package papermark

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/holman/deckk/internal/adapter"
	"github.com/holman/deckk/internal/adapter/imagefetch"
)

func init() {
	adapter.Register(&Adapter{})
}

// Adapter handles papermark.com/view/... links.
//
// Papermark's viewer is a React app that, on mount, POSTs to /api/views and
// gets back a JSON blob describing the deck: a view id plus one entry per
// page, each with a signed image URL (for PDFs that Papermark has already
// rasterised). Only the first ~10 pages come with URLs; the rest are signed
// on demand via /api/views/pages. Rather than paging through the viewer and
// screenshotting, we load the page once, catch that /api/views response off
// the wire, ask for the remaining page URLs in a couple of batches, and
// download the page images directly. Papermark records a single view, and
// only page 1 gets a "viewed" event — about the lightest touch possible.
type Adapter struct{}

func (a *Adapter) Name() string { return "papermark" }

func (a *Adapter) Matches(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	if (u.Scheme != "https" && u.Scheme != "http") || !(host == "papermark.com" || strings.HasSuffix(host, ".papermark.com") || host == "papermark.io" || strings.HasSuffix(host, ".papermark.io")) {
		return false
	}
	return strings.HasPrefix(u.Path, "/view/")
}

// viewsResponse is the subset of Papermark's POST /api/views response we
// care about.
type viewsResponse struct {
	Type            string          `json:"type"` // "email-verification" when the link needs an OTP
	Message         string          `json:"message"`
	WatermarkConfig json.RawMessage `json:"watermarkConfig"`
	ViewID          string          `json:"viewId"`
	File            string          `json:"file"`
	FileType        string          `json:"fileType"`
	Pages           []pageEntry     `json:"pages"`
}

type pageEntry struct {
	File       *string     `json:"file"`
	PageNumber json.Number `json:"pageNumber"`
}

func (p pageEntry) num() int {
	n, _ := strconv.Atoi(p.PageNumber.String())
	return n
}

// viewsRequest is what the viewer sends to /api/views; we need the
// document version id to ask for the lazily-signed pages.
type viewsRequest struct {
	DocumentVersionID string `json:"documentVersionId"`
	LinkID            string `json:"linkId"`
	ViewType          string `json:"viewType"`
}

// apiCall is one captured POST /api/views exchange.
type apiCall struct {
	Req      viewsRequest
	Resp     viewsResponse
	Status   int
	Endpoint string
	Err      error
}

// Papermark signs at most this many page URLs per /api/views/pages call.
const maxPagesPerRequest = 15

func (a *Adapter) Fetch(ctx context.Context, rawURL string, opts adapter.Options) ([]adapter.Slide, error) {
	result, err := a.FetchDocument(ctx, rawURL, opts)
	if err != nil {
		return nil, err
	}
	if result.PDF != nil {
		return nil, fmt.Errorf("native PDF available; use FetchDocument")
	}
	return result.Slides, nil
}

func (a *Adapter) FetchDocument(ctx context.Context, rawURL string, opts adapter.Options) (adapter.Document, error) {
	allocOpts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.UserAgent("Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
	)
	if opts.Headful {
		allocOpts = append(allocOpts, chromedp.Flag("headless", false))
	}

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, allocOpts...)
	defer cancelAlloc()

	bctx, cancelB := chromedp.NewContext(allocCtx)
	defer cancelB()

	calls := make(chan apiCall, 4)
	listenForViewsAPI(bctx, calls)

	if err := chromedp.Run(bctx,
		network.Enable(),
		chromedp.EmulateViewport(1600, 1200),
		chromedp.Navigate(rawURL),
	); err != nil {
		return adapter.Document{}, fmt.Errorf("navigate: %w", err)
	}

	return fetchDocument(bctx, calls, opts)
}

func fetchSlides(bctx context.Context, calls <-chan apiCall, opts adapter.Options) ([]adapter.Slide, error) {
	call, err := waitForDeck(bctx, calls, opts)
	if err != nil {
		return nil, err
	}
	return processSlides(bctx, call, opts)
}

func processSlides(bctx context.Context, call apiCall, opts adapter.Options) ([]adapter.Slide, error) {
	resp := call.Resp
	if len(resp.Pages) == 0 {
		return nil, fmt.Errorf("Papermark document is not served as page images; raw-file documents are not supported yet")
	}

	pages := resp.Pages
	sort.Slice(pages, func(i, j int) bool { return pages[i].num() < pages[j].num() })
	for i, p := range pages {
		if p.num() != i+1 {
			return nil, fmt.Errorf("incomplete Papermark page manifest: expected page %d, got %d", i+1, p.num())
		}
	}
	fmt.Fprintf(os.Stderr, "deck has %d page(s)\n", len(pages))

	// Ask for URLs for any pages the initial response left unsigned.
	var missing []int
	for _, p := range pages {
		if p.File == nil || *p.File == "" {
			missing = append(missing, p.num())
		}
	}
	if len(missing) > 0 {
		if call.Req.DocumentVersionID == "" || resp.ViewID == "" {
			return nil, fmt.Errorf("%d page(s) need signing but the view/version ids weren't captured", len(missing))
		}
		fmt.Fprintf(os.Stderr, "requesting URLs for %d more page(s)…\n", len(missing))
		signed, err := fetchPageURLs(bctx, resp.ViewID, call.Req.DocumentVersionID, missing)
		if err != nil {
			return nil, err
		}
		for i := range pages {
			if u, ok := signed[pages[i].num()]; ok && (pages[i].File == nil || *pages[i].File == "") {
				u := u
				pages[i].File = &u
			}
		}
	}

	client := &http.Client{Timeout: 60 * time.Second}
	slides := make([]adapter.Slide, 0, len(pages))
	for _, p := range pages {
		if p.File == nil || *p.File == "" {
			return nil, fmt.Errorf("no URL for page %d", p.num())
		}
		slide, err := imagefetch.Download(bctx, client, *p.File)
		var httpErr *imagefetch.HTTPError
		if errors.As(err, &httpErr) && (httpErr.Status == 401 || httpErr.Status == 403) && resp.ViewID != "" && call.Req.DocumentVersionID != "" {
			// Renew an expired image URL using the existing authenticated view.
			signed, signErr := fetchPageURLs(bctx, resp.ViewID, call.Req.DocumentVersionID, []int{p.num()})
			if signErr != nil {
				return nil, fmt.Errorf("refresh page %d: %w", p.num(), signErr)
			}
			if fresh := signed[p.num()]; fresh != "" {
				slide, err = imagefetch.Download(bctx, client, fresh)
			}
		}
		if err != nil {
			return nil, fmt.Errorf("download page %d: %w", p.num(), err)
		}
		slides = append(slides, slide)
	}

	if len(slides) == 0 {
		return nil, fmt.Errorf("no slides captured")
	}
	return slides, nil
}

// listenForViewsAPI watches network traffic for the viewer's POST /api/views
// and, once each one finishes, fetches its body and pushes the parsed
// exchange onto calls.
func listenForViewsAPI(bctx context.Context, calls chan<- apiCall) {
	var mu sync.Mutex
	pending := map[network.RequestID]viewsRequest{}
	endpoints := map[network.RequestID]string{}
	statuses := map[network.RequestID]int{}

	chromedp.ListenTarget(bctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			if e.Request == nil || e.Request.Method != "POST" {
				return
			}
			u, err := url.Parse(e.Request.URL)
			if err != nil || (u.Path != "/api/views" && u.Path != "/api/views-dataroom") {
				return
			}
			var req viewsRequest
			for _, entry := range e.Request.PostDataEntries {
				if b, err := base64.StdEncoding.DecodeString(entry.Bytes); err == nil {
					_ = json.Unmarshal(b, &req)
				}
			}
			mu.Lock()
			pending[e.RequestID] = req
			endpoints[e.RequestID] = u.Path
			mu.Unlock()

		case *network.EventResponseReceived:
			mu.Lock()
			if _, ok := pending[e.RequestID]; ok && e.Response != nil {
				statuses[e.RequestID] = int(e.Response.Status)
			}
			mu.Unlock()
		case *network.EventLoadingFinished:
			mu.Lock()
			req, ok := pending[e.RequestID]
			status := statuses[e.RequestID]
			endpoint := endpoints[e.RequestID]
			delete(endpoints, e.RequestID)
			delete(statuses, e.RequestID)
			if ok {
				delete(pending, e.RequestID)
			}
			mu.Unlock()
			if !ok {
				return
			}
			// Don't block the event loop: fetch the body on its own goroutine.
			go func(id network.RequestID) {
				c := chromedp.FromContext(bctx)
				execCtx := cdp.WithExecutor(bctx, c.Target)
				body, err := network.GetResponseBody(id).Do(execCtx)
				if req.DocumentVersionID == "" {
					if post, postErr := network.GetRequestPostData(id).Do(execCtx); postErr == nil {
						_ = json.Unmarshal([]byte(post), &req)
					}
				}
				call := apiCall{Req: req, Status: status, Endpoint: endpoint}
				if err != nil {
					call.Err = fmt.Errorf("read /api/views response: %w", err)
				} else if err := json.Unmarshal(body, &call.Resp); err != nil {
					call.Err = fmt.Errorf("parse /api/views response: %w", err)
				}
				select {
				case calls <- call:
				case <-bctx.Done():
				}
			}(e.RequestID)

		case *network.EventLoadingFailed:
			mu.Lock()
			_, ok := pending[e.RequestID]
			if ok {
				delete(pending, e.RequestID)
				delete(statuses, e.RequestID)
				delete(endpoints, e.RequestID)
			}
			mu.Unlock()
			if ok {
				select {
				case calls <- apiCall{Err: fmt.Errorf("/api/views request failed: %s", e.ErrorText)}:
				case <-bctx.Done():
				}
			}
		}
	})
}

// fetchPageURLsJS runs inside the page so the request rides on the viewer's
// own origin and cookies, exactly as the real client does when scrolling.
const fetchPageURLsJS = `
async (viewId, documentVersionId, pageNumbers) => {
  const r = await fetch('/api/views/pages', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ viewId, documentVersionId, pageNumbers }),
  });
  const text = await r.text();
  return { status: r.status, body: text };
}
`

type fetchResult struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

func fetchPageURLs(ctx context.Context, viewID, versionID string, nums []int) (map[int]string, error) {
	out := map[int]string{}
	for start := 0; start < len(nums); start += maxPagesPerRequest {
		end := start + maxPagesPerRequest
		if end > len(nums) {
			end = len(nums)
		}
		batch := nums[start:end]
		numsJSON, _ := json.Marshal(batch)
		script := fmt.Sprintf("(%s)(%q, %q, %s)", strings.TrimSpace(fetchPageURLsJS), viewID, versionID, numsJSON)

		var res fetchResult
		if err := chromedp.Run(ctx, chromedp.Evaluate(script, &res, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
			return p.WithAwaitPromise(true)
		})); err != nil {
			return nil, fmt.Errorf("request page URLs: %w", err)
		}
		if res.Status != 200 {
			return nil, fmt.Errorf("/api/views/pages returned HTTP %d; the view may have expired or access was denied", res.Status)
		}
		var parsed struct {
			Pages []pageEntry `json:"pages"`
		}
		if err := json.Unmarshal([]byte(res.Body), &parsed); err != nil {
			return nil, fmt.Errorf("parse /api/views/pages: %w", err)
		}
		wanted := make(map[int]bool, len(batch))
		for _, n := range batch {
			wanted[n] = true
		}
		for _, p := range parsed.Pages {
			if !wanted[p.num()] || p.File == nil || *p.File == "" {
				return nil, fmt.Errorf("invalid or duplicate Papermark signed page %d", p.num())
			}
			delete(wanted, p.num())
			out[p.num()] = *p.File
		}
		if len(wanted) > 0 {
			return nil, fmt.Errorf("Papermark omitted %d requested page URL(s)", len(wanted))
		}
	}
	return out, nil
}
