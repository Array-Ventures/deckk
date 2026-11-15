package browserskill

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func inspectProvider(ctx context.Context, source, provider, rawURL string) (map[string]interface{}, error) {
	hint, _ := json.Marshal(provider)
	// A lexical location represents a provider URL without contacting that provider.
	prefix := ""
	if rawURL != "" {
		address, _ := json.Marshal(rawURL)
		prefix = fmt.Sprintf("const location = new URL(%s);", address)
	}
	expression := fmt.Sprintf("(async()=>{%s return await (%s)(%s)})()", prefix, source, hint)
	var result map[string]interface{}
	err := chromedp.Run(ctx, chromedp.Evaluate(expression, &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
	return result, err
}

func TestProviderInspectors(t *testing.T) {
	source, err := os.ReadFile("../../skills/deckk/scripts/viewer_inspect.js")
	if err != nil {
		t.Fatal(err)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	image := `<svg xmlns="http://www.w3.org/2000/svg" width="640" height="360"><rect width="640" height="360" fill="blue"/></svg>`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slide.svg" {
			w.Header().Set("Content-Type", "image/svg+xml")
			fmt.Fprint(w, image)
			return
		}
		t.Errorf("Unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()
	slide := `<img class="viewer-image-mobile" alt="Page 1" src="` + server.URL + `/slide.svg" width="640" height="360">`
	fixtures := []struct {
		name, provider, html, status, gate, url string
		total                                   float64
		export                                  string
	}{
		{name: "Papermark screenshot", provider: "papermark", html: `<div style="position:relative;width:640px;height:360px">` + slide + `<div style="position:absolute;top:0;color:red">WATERMARK</div></div><div>1 / 3</div>`, status: "ready", total: 3},
		{name: "Papermark split counter", provider: "papermark", html: `<div style="width:640px;height:360px">` + slide + `</div><div style="display:flex"><span>1</span><span>/</span><span>3</span></div>`, status: "ready", total: 3},
		{name: "Papermark mismatch", provider: "papermark", html: `<div>` + strings.Replace(slide, "Page 1", "Page 2", 1) + `</div><div>1 / 3</div>`, status: "loading"},
		{name: "Papermark missing total", provider: "papermark", html: `<div>` + slide + `</div>`, status: "unsupported"},
		{name: "Papermark native single", provider: "papermark", html: `<a download href="/deck.pdf">Download PDF</a>`, status: "native-only"},
		{name: "Papermark custom fields", provider: "papermark", html: `<form><label>Company<input name="company" required></label><textarea name="reason"></textarea><button>Continue</button></form>`, status: "gate", gate: "credentials"},
		{name: "Papermark credential values hidden", provider: "papermark", html: `<form><input type="email" value="private@example.test"><input type="password" value="fixture-sensitive"><input name="name"><button>Continue</button></form>`, status: "gate", gate: "credentials"},
		{name: "Papermark OTP", provider: "papermark", html: `<form><input name="code" autocomplete="one-time-code"><button>Verify</button></form>`, status: "gate", gate: "verification-code"},
		{name: "Papermark rejected", provider: "papermark", html: `<form><input type="password"><p role="alert">Incorrect password</p><button>Continue</button></form>`, status: "gate", gate: "rejected"},
		{name: "Papermark agreement", provider: "papermark", html: `<form><input type="email"><button id="agreement" role="checkbox">Agree</button><button>Continue</button></form>`, status: "gate", gate: "agreement"},
		{name: "Papermark signing", provider: "papermark", html: `<button>Open signing</button>`, status: "gate", gate: "agreement"},
		{name: "Papermark sent email", provider: "papermark", html: `<h1>Check your inbox</h1>`, status: "gate", gate: "verification-link"},
		{name: "Pitch DOM SVG", provider: "pitch", html: `<div class="slide-wrapper" style="width:640px;height:360px"><svg width="640" height="360"><text x="20" y="20">Title</text></svg></div><div class="player-v2-chrome-controls-slide-count">1 / 4</div>`, status: "ready", total: 4},
		{name: "Pitch clipped", provider: "pitch", html: `<div class="slide-wrapper" style="width:640px;height:360px;transform:translateX(-50px)">Slide</div><div>1 / 4</div>`, status: "loading"},
		{name: "Pitch missing total", provider: "pitch", html: `<div class="slide-wrapper" style="width:640px;height:360px">Slide</div>`, status: "unsupported"},
		{name: "Pitch native", provider: "pitch", html: `<button aria-label="Download PDF">Export</button>`, status: "native-only"},
		{name: "Pitch media", provider: "pitch", html: `<div class="slide-wrapper" style="width:640px;height:360px"><video></video></div><div>1 / 4</div>`, status: "loading"},
		{name: "Canva page", provider: "canva", html: `<div data-page-id="0" style="width:640px;height:360px">Page</div><button>1 / 5</button>`, status: "ready", total: 5},
		{name: "Canva slider", provider: "canva", html: `<div data-page-id="1" style="width:640px;height:360px">Page</div><div role="slider" aria-label="Page" aria-valuenow="2" aria-valuemax="5">Pages</div>`, status: "ready", total: 5},
		{name: "Canva mismatch", provider: "canva", html: `<div data-page-id="1" style="width:640px;height:360px">Page</div><button>1 / 5</button>`, status: "loading"},
		{name: "Canva zoom is not pages", provider: "canva", html: `<div data-page-id="0" style="width:640px;height:360px">Page</div><div role="slider" aria-label="Zoom" aria-valuenow="1" aria-valuemax="5">Zoom</div>`, status: "unsupported"},
		{name: "Auto-detect Pitch", html: `<div class="slide-wrapper" style="width:640px;height:360px">Slide</div><div>1 / 3</div>`, status: "ready", total: 3, url: "https://pitch.com/v/example"},
		{name: "Auto-detect Slides", html: `<p>Presentation</p>`, status: "native-only", url: "https://docs.google.com/presentation/d/example_ID/edit", export: "https://docs.google.com/presentation/d/example_ID/export/pdf"},
		{name: "Unrecognized domain", html: `<p>Not a known viewer</p>`, status: "unsupported", url: "https://example.test/deck"},

		{name: "Slides export", provider: "google-slides", html: `<h1>Presentation</h1>`, status: "native-only", url: "https://docs.google.com/presentation/d/example_ID/edit", export: "https://docs.google.com/presentation/d/example_ID/export/pdf"},
		{name: "Slides published", provider: "google-slides", html: `<p>Published</p>`, status: "native-only", url: "https://docs.google.com/presentation/d/e/published_ID/pub", export: "https://docs.google.com/presentation/d/e/published_ID/export/pdf"},
		{name: "Slides wrong host", provider: "google-slides", html: `<h1>Presentation</h1>`, status: "native-only", url: "https://example.test/presentation/d/example_ID/edit"},
		{name: "Slides denied", provider: "google-slides", html: `<h1>You need access</h1>`, status: "gate", gate: "access-denied", url: "https://docs.google.com/presentation/d/example_ID/edit"},
		{name: "Drive path", provider: "google-drive", html: `<p>PDF</p>`, status: "native-only", url: "https://drive.google.com/file/d/file_ID/view", export: "https://drive.usercontent.google.com/download?id=file_ID&export=download"},
		{name: "Drive query", provider: "google-drive", html: `<p>PDF</p>`, status: "native-only", url: "https://drive.google.com/open?id=file_ID", export: "https://drive.usercontent.google.com/download?id=file_ID&export=download"},
		{name: "Drive rejects injected ID", provider: "google-drive", html: `<p>File</p>`, status: "native-only", url: "https://drive.google.com/open?id=bad%26export=other"},
		{name: "Direct PDF", provider: "pdf", html: `<a download href="/deck.pdf">Download PDF</a>`, status: "native-only"},
		{name: "Challenge", provider: "papermark", html: `<h1>Verify you are human</h1>`, status: "gate", gate: "challenge"},
		{name: "Notion scroll", provider: "notion", html: `<div class="notion-scroller vertical" style="width:640px;height:360px;overflow:auto"><div class="notion-page-content" style="height:900px"><div data-block-id="x">Document</div></div></div>`, status: "scroll-ready"},
		{name: "Notion missing scroller", provider: "notion", html: `<div class="notion-page-content">Document</div>`, status: "unsupported"},
	}
	for _, tc := range fixtures {
		t.Run(tc.name, func(t *testing.T) {
			page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("Inspector submitted access form")
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<style>body{margin:0}</style>`+tc.html)
			}))
			defer page.Close()
			ctx, cancel := context.WithTimeout(browser, 10*time.Second)
			defer cancel()
			if err := chromedp.Run(ctx, chromedp.Navigate(page.URL), chromedp.Poll(`Array.from(document.images).every(i=>i.complete)`, nil)); err != nil {
				t.Fatal(err)
			}
			result, err := inspectProvider(ctx, string(source), tc.provider, tc.url)
			if err != nil {
				t.Fatal(err)
			}
			if result["status"] != tc.status {
				t.Fatalf("state=%v", result)
			}
			if tc.gate != "" && result["gate"].(map[string]interface{})["kind"] != tc.gate {
				t.Fatalf("gate=%v", result)
			}
			if tc.total > 0 && result["total"] != tc.total {
				t.Fatalf("counter=%v", result)
			}
			if tc.export != "" && result["exportURL"] != tc.export {
				t.Fatalf("export=%v", result)
			}
			if strings.Contains(tc.name, "wrong host") || strings.Contains(tc.name, "injected ID") {
				if result["exportURL"] != nil {
					t.Fatalf("unexpected URL=%v", result)
				}
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "fixture-sensitive") || strings.Contains(string(encoded), "private@example.test") {
				t.Fatal("Inspector leaked input values")
			}
			if tc.status == "ready" {
				again, err := inspectProvider(ctx, string(source), tc.provider, tc.url)
				if err != nil {
					t.Fatal(err)
				}
				if again["fingerprint"] != result["fingerprint"] {
					t.Fatal("Selector markers changed the render fingerprint")
				}
			}
		})
	}
}
