package canva

import (
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"

	"github.com/holman/deckk/internal/adapter"
)

func init() {
	adapter.Register(&Adapter{})
}

type Adapter struct{}

func (a *Adapter) Name() string { return "canva" }

func (a *Adapter) Matches(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	// canva.link is Canva's short-link domain ("Share → Copy link" hands
	// out these). It 302s to the full canva.com/design/.../view URL, which
	// Chrome follows for us.
	return host == "canva.com" || strings.HasSuffix(host, ".canva.com") ||
		host == "canva.link" || strings.HasSuffix(host, ".canva.link")
}

type viewportRect struct {
	X, Y, W, H float64
}

// activePageJS selects the same fully visible page for both navigation checks
// and cropping. Offscreen mounted pages and thumbnails are not active slides.
const activePageJS = `
(() => Array.from(document.querySelectorAll('[data-page-id]'))
  .filter(el => {
    const r = el.getBoundingClientRect();
    const s = getComputedStyle(el);
    return /^\d+$/.test(el.dataset.pageId) && Number.isSafeInteger(Number(el.dataset.pageId)) &&
      r.width >= 400 && r.height >= 250 && r.left >= 0 && r.top >= 0 &&
      r.right <= innerWidth + 1 && r.bottom <= innerHeight + 1 &&
      r.width / r.height >= 0.5 && r.width / r.height <= 3 &&
      s.visibility !== 'hidden' && s.opacity !== '0';
  })
  .sort((a, b) => {
    const ar = a.getBoundingClientRect(), br = b.getBoundingClientRect();
    return br.width * br.height - ar.width * ar.height;
  })[0] || null)()
`

// detectPlayerJS returns the visible slide bounds. Legacy players use a
// cluster of equally sized layers; its size never determines the page count.
const detectPlayerJS = `
(() => {
  // The newer anonymous viewer uses an editor-like shell with a thumbnail
  // filmstrip. Its presentation region bounds the slide, not the whole shell.
  const active = (` + activePageJS + `);
  if (active) {
    const r = active.getBoundingClientRect();
    return { x: r.x, y: r.y, w: r.width, h: r.height };
  }
  // A modern viewer with no fully visible page must not fall through to
  // screenshotting the surrounding application shell.
  if (document.querySelector('[data-page-id]')) return null;
  const all = Array.from(document.querySelectorAll('div, section, article, figure'));
  const buckets = new Map();
  for (const el of all) {
    const r = el.getBoundingClientRect();
    if (r.width < 400 || r.height < 250) continue;
    const ar = r.width / r.height;
    if (ar < 0.5 || ar > 3) continue;
    const key = Math.round(r.width) + 'x' + Math.round(r.height) + '@' + Math.round(r.left) + ',' + Math.round(r.top);
    if (!buckets.has(key)) buckets.set(key, { rect: r, els: [] });
    buckets.get(key).els.push(el);
  }
  let best = null;
  for (const v of buckets.values()) {
    if (!best || v.els.length > best.els.length) best = v;
  }
  if (!best || best.els.length < 1) return null;

  const r = best.rect;
  return {
    x: r.x, y: r.y, w: r.width, h: r.height,
  };
})()
`

// hideOverlaysJS strips chrome that would otherwise leak into the
// screenshots: cookie banner, persistent CTAs, Canva's top nav, chat
// widgets. As a final pass it sweeps any fixed/sticky element pinned to
// the edges with consent-y text, which catches OneTrust / TrustArc /
// Cookiebot / Canva's own variants without us enumerating every selector.
const hideOverlaysJS = `
(() => {
  const hide = el => el && el.style.setProperty('display', 'none', 'important');
  const modern = Array.from(document.querySelectorAll('[data-page-id]'))
    .some(el => { const r = el.getBoundingClientRect(); return r.width >= 400 && r.height >= 250; });
  const selectors = [
    '#onetrust-banner-sdk',
    '#onetrust-consent-sdk',
    '#truste-consent-track',
    '#truste-consent-content',
    '[id*="cookie" i][class*="banner" i]',
    '[class*="cookie" i][class*="banner" i]',
    '[data-testid*="cookie" i]',
    '[data-testid*="consent" i]',
    'header',
    '[role="banner"]',
    '[data-testid*="header" i]',
    '[data-testid*="top-bar" i]',
    '[data-testid*="signup" i]',
    '[data-testid*="sign-up" i]',
    '[data-testid*="cta" i]',
    '[data-testid*="footer" i]',
    'footer',
    '#intercom-frame', '.intercom-launcher-frame',
    '#hubspot-messages-iframe-container',
  ];
  for (const sel of selectors) {
    // The anonymous editor shell virtualizes hidden controls, including its
    // page counter. Its chrome already sits outside our presentation crop.
    if (modern && !/cookie|consent|truste|onetrust|intercom|hubspot/i.test(sel)) continue;
    document.querySelectorAll(sel).forEach(hide);
  }

  // Consent iframes by src (OneTrust/TrustArc/Cookiebot/etc).
  document.querySelectorAll('iframe').forEach(f => {
    const src = (f.src || '') + ' ' + (f.id || '') + ' ' + (typeof f.className === 'string' ? f.className : '');
    if (/onetrust|trustarc|cookiebot|consent|cookie|gdpr|ccpa/i.test(src)) hide(f);
  });

  // Last-resort cookie sweep: any fixed/sticky element near a viewport
  // edge with consent-y text. Match on text content so renamed/obfuscated
  // banners still get caught.
  const needles = /(cookie|consent|gdpr|accept all|manage preferences|privacy choices)/i;
  const vh = window.innerHeight;
  document.querySelectorAll('div, section, aside').forEach(el => {
    const pos = getComputedStyle(el).position;
    if (pos !== 'fixed' && pos !== 'sticky') return;
    const r = el.getBoundingClientRect();
    if (r.height > vh * 0.6) return;
    const nearEdge = r.bottom > vh * 0.5 || r.top < vh * 0.2;
    if (!nearEdge) return;
    const txt = (el.textContent || '').slice(0, 400);
    if (needles.test(txt)) hide(el);
  });

  // Floating action buttons (Present, Share, Edit, …) — walk up to the
  // nearest small floating wrapper so we hide just the chip, not its
  // anchored container.
  if (modern) return;
  for (const sel of [
    'button[aria-label*="Present" i]',
    'button[aria-label*="Share" i]',
    'button[aria-label*="Download" i]',
    'button[aria-label*="Sign" i]',
    'button[aria-label*="Edit" i]',
  ]) {
    document.querySelectorAll(sel).forEach(b => {
      let n = b;
      for (let i = 0; i < 4 && n; i++) {
        const p = getComputedStyle(n).position;
        if (p === 'fixed' || p === 'sticky' || p === 'absolute') { hide(n); return; }
        n = n.parentElement;
      }
      hide(b);
    });
  }
})()
`

// pageStateJS reads Canva's own idea of where we are in the deck. The
// /view player exposes a "Go to page" slider (role=slider) whose
// aria-valuemax is the real page count and aria-valuenow the current
// page. The URL hash also tracks the current page ("#3"). Both are far
// more trustworthy than counting stacked DOM boxes, so we prefer them and
// only fall back to the cluster heuristic if neither is present.
const pageStateJS = `
(() => {
  let total = 0, current = 0;
  const sl = Array.from(document.querySelectorAll('[role="slider"]'))
    .find(el => /page/i.test(el.getAttribute('aria-label') || ''));
  if (sl) {
    total = parseInt(sl.getAttribute('aria-valuemax') || '0', 10) || 0;
    current = parseInt(sl.getAttribute('aria-valuenow') || '0', 10) || 0;
  }
  if (!total) {
    // Read textContent even after the footer is hidden for screenshots. The
    // page counter may be a button; the only range input can be Zoom.
    for (const el of document.querySelectorAll('button, [role="button"]')) {
      const m = (el.textContent || '').trim().match(/^(\d+)\s*\/\s*(\d+)$/);
      if (m) { current = parseInt(m[1], 10); total = parseInt(m[2], 10); break; }
    }
  }
  // The anonymous viewer can unmount the counter text while idle. Its
  // full-size page node still exposes the current zero-based page index.
  const active = (` + activePageJS + `);
  if (active) {
    current = Number(active.dataset.pageId) + 1;
    if (total) window.__deckkCanvaPageTotal = total;
    else total = window.__deckkCanvaPageTotal || 0;
  }
  const hm = (location.hash || '').match(/^#(\d+)$/);
  const hashPage = hm ? parseInt(hm[1], 10) : 0;
  if (!current && hashPage) current = hashPage;
  return { total, current, hashPage };
})()
`

type pageState struct {
	Total    int `json:"total"`
	Current  int `json:"current"`
	HashPage int `json:"hashPage"`
}

// Timing knobs for the animation wait. Canva plays each page's element
// animations (fade/rise/pan…) on entry and drives them from JS, so there's
// no DOM or Web Animations signal to hook — the only honest "done" is
// "the pixels stopped changing". We poll clipped screenshots and accept a
// frame once it has repeated stableSamples times in a row. maxSettle caps
// how long a looping element (GIF, video, infinite animation) can hold us
// up; after that we take the latest frame and move on.
const (
	settlePoll    = 300 * time.Millisecond
	stableSamples = 3
	maxSettle     = 12 * time.Second
	pageFlipWait  = 3 * time.Second
)

func (a *Adapter) Fetch(ctx context.Context, rawURL string, opts adapter.Options) ([]adapter.Slide, error) {
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

	if err := chromedp.Run(bctx,
		chromedp.EmulateViewport(1600, 1200),
		chromedp.Navigate(rawURL),
		chromedp.Sleep(5*time.Second),
		chromedp.Poll(`(`+pageStateJS+`).total > 0`, nil, chromedp.WithPollingTimeout(20*time.Second)),
		chromedp.Evaluate(hideOverlaysJS, nil),
	); err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}

	var det *viewportRect
	if err := chromedp.Run(bctx, chromedp.Evaluate(detectPlayerJS, &det)); err != nil {
		return nil, fmt.Errorf("detect player: %w", err)
	}
	if det == nil || det.W <= 0 || det.H <= 0 {
		return nil, fmt.Errorf("could not find slide player on the page")
	}

	var st pageState
	if err := chromedp.Run(bctx, chromedp.Evaluate(pageStateJS, &st)); err != nil {
		return nil, fmt.Errorf("read page state: %w", err)
	}
	// A DOM cluster counts mounted nodes, not pages. Canva virtualizes longer
	// decks, so exporting that estimate can silently truncate a presentation.
	total := st.Total
	if total <= 0 {
		return nil, fmt.Errorf("could not determine Canva page count; no partial PDF was saved")
	}
	fmt.Fprintf(os.Stderr, "deck has %d slide(s)\n", total)

	var slides []adapter.Slide
	var lastHash [32]byte
	for i := 1; i <= total; i++ {
		if i > 1 {
			if err := chromedp.Run(bctx, chromedp.KeyEvent(kb.ArrowRight)); err != nil {
				return nil, fmt.Errorf("advance to slide %d: %w", i, err)
			}
		}
		if err := waitForPage(bctx, i); err != nil {
			return nil, fmt.Errorf("expected slide %d: %w", i, err)
		}
		if err := chromedp.Run(bctx, chromedp.Evaluate(hideOverlaysJS, nil)); err != nil {
			return nil, err
		}
		if err := chromedp.Run(bctx, chromedp.Evaluate(detectPlayerJS, &det)); err != nil {
			return nil, err
		}
		if det == nil || det.W <= 0 || det.H <= 0 {
			return nil, fmt.Errorf("slide %d vanished; no partial PDF was saved", i)
		}
		playerRect := viewportRect{X: det.X, Y: det.Y, W: det.W, H: det.H}
		shot, err := captureSettled(bctx, playerRect)
		if err != nil {
			return nil, fmt.Errorf("screenshot slide %d: %w", i, err)
		}
		h := sha256.Sum256(shot)
		if i > 1 && h == lastHash {
			// The counter confirmed this page; retain intentional duplicates.
			fmt.Fprintf(os.Stderr, "slide %d looks identical to slide %d; keeping it\n", i, i-1)
		}
		lastHash = h
		slides = append(slides, adapter.Slide{Data: shot, ContentType: "image/png"})
		fmt.Fprintf(os.Stderr, "captured slide %d/%d\n", i, total)
	}

	if len(slides) == 0 {
		return nil, fmt.Errorf("no slides captured")
	}
	return slides, nil
}

// waitForPage blocks until Canva reports page n as current (slider value
// or URL hash), so a slow page flip can't be mistaken for "no change".
func waitForPage(ctx context.Context, n int) error {
	deadline := time.Now().Add(pageFlipWait)
	for {
		var st pageState
		if err := chromedp.Run(ctx, chromedp.Evaluate(pageStateJS, &st)); err != nil {
			return err
		}
		if st.Current == n {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("player stuck on page %d (wanted %d)", st.Current, n)
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
			return err
		}
	}
}

// captureSettled screenshots the player repeatedly until the image stops
// changing (see the settle constants above), then returns the final frame.
// This is how we wait out Canva's entry animations without knowing
// anything about them.
func captureSettled(ctx context.Context, r viewportRect) ([]byte, error) {
	deadline := time.Now().Add(maxSettle)
	var (
		last    [32]byte
		shot    []byte
		repeats int
	)
	for {
		var err error
		shot, err = captureViewportClip(ctx, r)
		if err != nil {
			return nil, err
		}
		h := sha256.Sum256(shot)
		if h == last {
			repeats++
			if repeats >= stableSamples-1 {
				return shot, nil
			}
		} else {
			repeats = 0
			last = h
		}
		if time.Now().After(deadline) {
			fmt.Fprintf(os.Stderr, "slide still animating after %s; capturing as-is\n", maxSettle)
			return shot, nil
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(settlePoll)); err != nil {
			return nil, err
		}
	}
}

func captureViewportClip(ctx context.Context, r viewportRect) ([]byte, error) {
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
				Scale:  2,
			}).
			Do(ctx)
		return err
	}))
	return buf, err
}
