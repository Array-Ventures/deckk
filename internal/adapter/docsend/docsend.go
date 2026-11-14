package docsend

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/holman/deckk/internal/adapter"
	"github.com/holman/deckk/internal/adapter/nativefile"
)

func init() {
	adapter.Register(&Adapter{})
}

type Adapter struct{}

func (a *Adapter) Name() string { return "docsend" }

func (a *Adapter) Matches(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return u.Scheme == "https" && isDocSendHost(u.Hostname())
}

var pageCounterRe = regexp.MustCompile(`(\d+)\s*/\s*(\d+)`)

// slideRect describes the currently-visible slide image in viewport pixels.
// Found=false means we walked the DOM and no candidate img was visible —
// typically the docsend upsell interstitial after the real deck.
type slideRect struct {
	Found      bool
	X, Y, W, H float64
	Src        string // slide URL, or a canvas dataURL fingerprint; used for change detection
	Ready      bool
}

// The IMG element is letterboxed (object-fit: contain) inside a taller
// container, so its bounding box doesn't match the actual rendered slide.
// Compute the real image area from naturalWidth/Height + the element box,
// so the clip is tight on the slide and not the surrounding white space.
//
// Slide discovery is layered so a DocSend viewer refresh doesn't silently
// break capture:
//  1. img[class*="page-view"] — the historic slide class (substring match
//     survives a design-system refresh that hashes the surrounding classes).
//  2. Structural: the slide is the dominant image in the viewer.
//  3. Canvas-rendered viewer.
//
// Src carries a change fingerprint (image URL, or a canvas dataURL prefix),
// used to detect end-of-deck when the viewer has no slide counter.
const findVisibleSlideJS = `
(() => {
  const vw = window.innerWidth, vh = window.innerHeight;
  const inView = (r) => r.width > 0 && r.height > 0 &&
    r.x < vw && r.y < vh && r.x + r.width > 0 && r.y + r.height > 0;
  const shown = (el) => {
    const cs = window.getComputedStyle(el);
    return cs.display !== 'none' && cs.visibility !== 'hidden' &&
      parseFloat(cs.opacity || '1') > 0;
  };
  const fpOf = (el, tag) => {
    if (tag === 'CANVAS') {
      try { return 'canvas:' + el.toDataURL().slice(0, 512); } catch (e) { return ''; }
    }
    return el.currentSrc || el.src || '';
  };
  const rectOf = (el, tag, minSize) => {
    const r = el.getBoundingClientRect();
    if (r.width < minSize || r.height < minSize || !inView(r) || !shown(el)) return null;
    let x = r.x, y = r.y, w = r.width, h = r.height, ready = true;
    if (tag === 'IMG') {
      const src = el.currentSrc || el.src || '';
      ready = el.complete && el.naturalWidth > 0 && !src.endsWith('blank.gif');
      const nw = el.naturalWidth || r.width, nh = el.naturalHeight || r.height;
      const scale = Math.min(r.width / nw, r.height / nh);
      const rw = nw * scale, rh = nh * scale;
      x = r.x + (r.width - rw) / 2; y = r.y + (r.height - rh) / 2; w = rw; h = rh;
    }
    return { x, y, w, h, fp: fpOf(el, tag), ready };
  };
  const cands = [];
  const push = (sel, tag, minSize) => {
    for (const el of document.querySelectorAll(sel)) {
      const src = (el.currentSrc || el.src || '').toLowerCase();
      // Decorative chrome (banners, logos, avatars, thumbnails) can be
      // large; never mistake it for the slide.
      if (/banner|logo|avatar|thumbnail|thumb|icon|powered-by/.test(src)) continue;
      const c = rectOf(el, tag, minSize);
      if (c) cands.push(c);
    }
  };
  push('img[class*="page-view"]', 'IMG', 50);
  if (!cands.length) push('img', 'IMG', 200);
  if (!cands.length) push('canvas', 'CANVAS', 200);
  if (!cands.length) return { found: false };
  cands.sort((a, b) => (b.w * b.h) - (a.w * a.h));
  const c = cands[0];
  return { found: true, x: c.x, y: c.y, w: c.w, h: c.h, src: c.fp, ready: c.ready };
})()
`

// hideOverlaysJS hides the cookie banner and any other floating UI
// (intercom chat, "next" arrow controls, etc.) so it doesn't bleed into
// the slide screenshots. We find the banner by text content and hide its
// nearest floating ancestor — broader rules risk taking out the slide
// viewer's own container.
const hideOverlaysJS = `
(() => {
  const isFloating = el => {
    const p = getComputedStyle(el).position;
    return p === 'fixed' || p === 'sticky' || p === 'absolute';
  };
  const hideFloatingAncestor = el => {
    let n = el;
    while (n && n !== document.body) {
      if (isFloating(n)) { n.style.setProperty('display', 'none', 'important'); return true; }
      n = n.parentElement;
    }
    return false;
  };

  // Cookie banner — match by visible text. Walk leaf-ish elements first
  // so we hide the banner itself, not a giant wrapper.
  const needles = ['we use cookies', 'cookie preferences', 'accept all cookies', 'manage cookies', 'privacy policy faqs'];
  const all = document.querySelectorAll('div, section, aside, footer, p, span');
  // Sort so leafier elements (shorter textContent) get checked first.
  const sorted = Array.from(all).sort((a, b) => (a.textContent || '').length - (b.textContent || '').length);
  for (const el of sorted) {
    const txt = (el.textContent || '').toLowerCase();
    if (txt && needles.some(n => txt.includes(n))) {
      if (hideFloatingAncestor(el)) break;
    }
  }

  // Iframe-based banners (e.g. Dropbox's CCPA iframe) — hide them too.
  for (const f of document.querySelectorAll('iframe')) {
    const src = f.src || '';
    if (/ccpa|consent|cookie|gdpr/i.test(src) || /ccpa|consent|cookie/i.test(f.id + ' ' + f.className)) {
      f.style.setProperty('display', 'none', 'important');
    }
  }

  // Common chat widgets that float on top of content.
  for (const sel of ['#intercom-frame', '.intercom-launcher-frame', '#hubspot-messages-iframe-container']) {
    document.querySelectorAll(sel).forEach(el => el.style.setProperty('display', 'none', 'important'));
  }

  // Hide the viewer's own navigation arrows so they don't appear on the
  // edges of our screenshots. We still drive navigation via keyboard.
  for (const sel of [
    '#nextPageButton', '#previousPageButton',
    '.carousel-control',
    '.preso-nav', '.preso-nav-left', '.preso-nav-right',
    'button[aria-label*="next" i]', 'button[aria-label*="previous" i]',
  ]) {
    document.querySelectorAll(sel).forEach(el => el.style.setProperty('display', 'none', 'important'));
  }
})()
`

func (a *Adapter) FetchDocument(ctx context.Context, rawURL string, opts adapter.Options) (adapter.Document, error) {
	slides, data, err := a.fetch(ctx, rawURL, opts)
	return adapter.Document{Slides: slides, PDF: data}, err
}

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

func (a *Adapter) fetch(ctx context.Context, rawURL string, opts adapter.Options) ([]adapter.Slide, []byte, error) {
	if opts.VerificationLink != "" {
		if _, err := verificationURL(opts.VerificationLink, rawURL); err != nil {
			return nil, nil, err
		}
	}
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

	// Generous viewport so slides render at decent resolution; the deviceScaleFactor
	// in CaptureScreenshot's clip gives us another quality bump.
	if err := chromedp.Run(bctx,
		chromedp.EmulateViewport(1600, 1200),
		chromedp.Navigate(rawURL),
		chromedp.Sleep(4*time.Second),
		chromedp.Evaluate(hideOverlaysJS, nil),
	); err != nil {
		return nil, nil, fmt.Errorf("navigate: %w", err)
	}

	// Clear access gates before looking for slides.
	if err := handleAccessGates(bctx, opts); err != nil {
		return nil, nil, err
	}

	// Establish the visitor session before consuming its email link.
	if opts.VerificationLink != "" {
		if err := completeVerification(bctx, rawURL, opts.VerificationLink, opts); err != nil {
			return nil, nil, err
		}
	}

	// DocSend "Space" links (/view/s/<id>) land on a space home page listing
	// documents after the gate, not a viewer. Step into the pitch deck.
	if isSpaceURL(rawURL) {
		if err := enterSpaceDocument(bctx); err != nil {
			return nil, nil, err
		}
		if err := handleAccessGates(bctx, opts); err != nil {
			return nil, nil, err
		}
	}

	if data, found, err := nativefile.Try(bctx); err != nil {
		return nil, nil, err
	} else if found && nativefile.IsPDF(data) {
		if err := nativefile.ValidatePDF(data); err != nil {
			return nil, nil, err
		}
		fmt.Fprintln(os.Stderr, "downloaded DocSend's native PDF")
		return nil, data, nil
	}

	total, err := detectTotalSlides(bctx)
	if err != nil {
		// The refreshed viewer doesn't always render a textual "n / N"
		// counter. Fall back to advancing until the slide image stops
		// changing, which is the normal end-of-deck signal.
		fmt.Fprintf(os.Stderr, "warning: %v; detecting end of deck by advancing\n", err)
		total = -1
	} else {
		fmt.Fprintf(os.Stderr, "deck has %d slide(s) per viewer\n", total)
	}
	if slides, supported, err := fetchOriginalSlides(bctx, total); supported || err != nil {
		if errors.Is(err, errVerificationRequired) && opts.VerificationLink == "" && opts.RequestVerificationLink != nil {
			link, promptErr := opts.RequestVerificationLink(bctx)
			if promptErr != nil {
				return nil, nil, promptErr
			}
			if verifyErr := completeVerification(bctx, rawURL, link, opts); verifyErr != nil {
				return nil, nil, verifyErr
			}
			if data, found, nativeErr := nativefile.Try(bctx); nativeErr != nil {
				return nil, nil, nativeErr
			} else if found && nativefile.IsPDF(data) {
				if err := nativefile.ValidatePDF(data); err != nil {
					return nil, nil, err
				}
				fmt.Fprintln(os.Stderr, "downloaded DocSend's native PDF")
				return nil, data, nil
			}
			slides, supported, err = fetchOriginalSlides(bctx, total)
			if !supported && err == nil {
				return nil, nil, fmt.Errorf("DocSend image endpoints unavailable after verification")
			}
		}
		if err == nil {
			fmt.Fprintln(os.Stderr, "downloaded original DocSend slide images")
		}
		return slides, nil, err
	}

	// Sanity cap for counter-less decks; real decks are far shorter.
	const maxSlides = 300
	slides := make([]adapter.Slide, 0)
	prevSrc := ""
	for i := 1; i <= maxSlides; i++ {
		if total > 0 && i > total {
			break
		}
		if i > 1 {
			if err := chromedp.Run(bctx,
				chromedp.KeyEvent(kb.ArrowRight),
				chromedp.Sleep(250*time.Millisecond),
			); err != nil {
				return nil, nil, fmt.Errorf("advance to slide %d: %w", i, err)
			}
		}

		// Cookie banner / next-arrow can re-render on navigation; re-hide
		// before each screenshot.
		if err := chromedp.Run(bctx, chromedp.Evaluate(hideOverlaysJS, nil)); err != nil {
			return nil, nil, err
		}

		var rect slideRect
		if total < 0 && i > 1 {
			r, err := waitForSlideChange(bctx, prevSrc)
			if err != nil {
				fmt.Fprintf(os.Stderr, "warning: stopped at slide %d — %v\n", i, err)
				break
			}
			rect = r
		} else if r, err := waitForSlideReady(bctx, i); err != nil {
			if total > 0 {
				return nil, nil, fmt.Errorf("incomplete DocSend deck at page %d/%d: %w", i, total, err)
			}
			// Past the real deck (placeholder never resolves to a real image)
			// is the normal stop condition; downstream is the docsend upsell.
			fmt.Fprintf(os.Stderr, "warning: stopped at slide %d — %v\n", i, err)
			break
		} else {
			rect = r
		}
		prevSrc = rect.Src

		png, err := captureClip(bctx, rect)
		if err != nil {
			return nil, nil, fmt.Errorf("screenshot slide %d: %w", i, err)
		}
		slides = append(slides, adapter.Slide{Data: png, ContentType: "image/png"})
	}
	if total < 0 && len(slides) >= maxSlides {
		return nil, nil, fmt.Errorf("hit %d-slide cap; deck may be longer than expected", maxSlides)
	}

	if len(slides) == 0 {
		return nil, nil, fmt.Errorf("no slides captured")
	}
	if total > 0 && len(slides) != total {
		return nil, nil, fmt.Errorf("incomplete DocSend deck: captured %d/%d slides", len(slides), total)
	}
	return slides, nil, nil
}

func detectTotalSlides(ctx context.Context) (int, error) {
	var bodyText string
	if err := chromedp.Run(ctx, chromedp.Text("body", &bodyText, chromedp.ByQuery)); err != nil {
		return 0, fmt.Errorf("read body: %w", err)
	}
	m := pageCounterRe.FindStringSubmatch(bodyText)
	if m == nil {
		var count int
		if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => {
  const explicit = [...document.querySelectorAll('[data-page-num]')].map(el => Number(el.getAttribute('data-page-num'))).filter(n => Number.isInteger(n) && n > 0);
  return explicit.length ? Math.max(...explicit) : document.querySelectorAll('.carousel-inner .item').length;
})()`, &count)); err != nil {
			return 0, err
		}
		if count > 0 && count <= 300 {
			return count, nil
		}
		return 0, fmt.Errorf("could not detect slide count (no n/N marker)")
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("bogus slide count %q", m[2])
	}
	return n, nil
}

// Space home pages (/view/s/<id>) list document links instead of rendering
// a viewer. Find the linked documents and step into the most deck-like one.
const spaceDocsJS = `
(() => {
  const docs = [];
  for (const a of document.querySelectorAll('a[href*="/view/"]')) {
    const href = a.getAttribute('href') || '';
    if (/\/view\/s\//.test(href)) continue; // the space itself, not a document
    const text = (a.textContent || '').trim().replace(/\s+/g, ' ').slice(0, 120);
    if (!text) continue;
    try { docs.push({ href: new URL(href, location.href).toString(), text }); }
    catch (e) { /* ignore malformed hrefs */ }
  }
  return docs;
})()`

var deckNameRe = regexp.MustCompile(`(?i)pitch|deck`)
var appendixRe = regexp.MustCompile(`(?i)appendix`)

type spaceDoc struct {
	Href string
	Text string
}

// isSpaceURL reports whether the link is a DocSend space (/view/s/<id>)
// rather than a single document. Only space links get the document-list
// treatment; a direct document viewer can carry unrelated /view/ anchors
// we must not follow.
func isSpaceURL(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return strings.HasPrefix(u.Path, "/view/s/")
}

// enterSpaceDocument navigates into the linked document from a DocSend
// space home. If no document links show up within a few seconds it
// returns nil and lets the normal slide capture try the page as-is.
// The pick prefers a "pitch"/"deck" document that isn't an appendix,
// falling back to the first listed document.
func enterSpaceDocument(ctx context.Context) error {
	deadline := time.Now().Add(8 * time.Second)
	for {
		var docs []spaceDoc
		if err := chromedp.Run(ctx, chromedp.Evaluate(spaceDocsJS, &docs)); err != nil {
			return err
		}
		if len(docs) > 0 {
			score := func(t string) int {
				s := 0
				if deckNameRe.MatchString(t) {
					s += 2
				}
				if appendixRe.MatchString(t) {
					s -= 3
				}
				return s
			}
			pick, best := docs[0], score(docs[0].Text)
			for _, d := range docs[1:] {
				if s := score(d.Text); s > best {
					pick, best = d, s
				}
			}
			fmt.Fprintf(os.Stderr, "space lists %d document(s); opening %q\n", len(docs), pick.Text)
			if err := chromedp.Run(ctx,
				chromedp.Navigate(pick.Href),
				chromedp.Sleep(4*time.Second),
				chromedp.Evaluate(hideOverlaysJS, nil),
			); err != nil {
				return fmt.Errorf("open space document: %w", err)
			}
			return nil
		}
		if time.Now().After(deadline) {
			return nil // no document links: direct document page
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(500*time.Millisecond)); err != nil {
			return err
		}
	}
}

// waitForSlideReady polls until the visible slide image has a real (non
// blank.gif) src loaded. Times out after ~15s. A missing image is treated
// as "not yet" until the deadline — DocSend lays slides out lazily and the
// first poll often races it.
func waitForSlideReady(ctx context.Context, n int) (slideRect, error) {
	deadline := time.Now().Add(15 * time.Second)
	var last slideRect
	for {
		var r slideRect
		if err := chromedp.Run(ctx, chromedp.Evaluate(findVisibleSlideJS, &r)); err != nil {
			return slideRect{}, err
		}
		last = r
		if r.Found && r.Ready && r.W > 0 {
			return r, nil
		}
		if time.Now().After(deadline) {
			if !last.Found {
				return slideRect{}, fmt.Errorf("no visible slide image (likely past the real deck)")
			}
			return slideRect{}, fmt.Errorf("slide image never loaded (src=%q)", lastSeg(last.Src))
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(300*time.Millisecond)); err != nil {
			return slideRect{}, err
		}
	}
}

// waitForSlideChange polls until the visible slide image has a loaded src
// different from prevSrc. Used when the viewer has no slide counter:
// advancing past the last slide leaves it in place, so an unchanging src
// is the end-of-deck signal. Times out after ~15s.
func waitForSlideChange(ctx context.Context, prevSrc string) (slideRect, error) {
	deadline := time.Now().Add(15 * time.Second)
	for {
		var r slideRect
		if err := chromedp.Run(ctx, chromedp.Evaluate(findVisibleSlideJS, &r)); err != nil {
			return slideRect{}, err
		}
		if r.Found && r.Ready && r.W > 0 && r.Src != "" && r.Src != prevSrc {
			return r, nil
		}
		if time.Now().After(deadline) {
			return slideRect{}, fmt.Errorf("slide did not advance past %q", lastSeg(prevSrc))
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(300*time.Millisecond)); err != nil {
			return slideRect{}, err
		}
	}
}

func captureClip(ctx context.Context, r slideRect) ([]byte, error) {
	var buf []byte
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		buf, err = page.CaptureScreenshot().
			WithFormat(page.CaptureScreenshotFormatPng).
			WithClip(&page.Viewport{
				X:      r.X,
				Y:      r.Y,
				Width:  r.W,
				Height: r.H,
				Scale:  2, // 2x for crisper PDFs
			}).
			Do(ctx)
		return err
	}))
	return buf, err
}

func lastSeg(s string) string {
	if i := strings.LastIndex(s, "/"); i >= 0 {
		return s[i+1:]
	}
	return s
}
