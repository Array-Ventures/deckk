package pitch

import (
	"context"
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

func (a *Adapter) Name() string { return "pitch" }

func (a *Adapter) Matches(rawURL string) bool {
	u, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Host)
	return host == "pitch.com" || strings.HasSuffix(host, ".pitch.com")
}

type viewportRect struct {
	X, Y, W, H float64
}

// findSlideJS returns the rect of the currently-visible slide plus the
// deck's slide counter. Pitch's public player (`/v/...`) renders the active
// slide into a `.slide-wrapper`/`.slide` element inside `.player-v2--stage`,
// at a clean 16:9 box, with the chrome (controls, counter) docked below it.
// The counter ("3 / 13") comes from the chrome's slide-count control; we
// fall back to scanning the body text if the class ever changes.
const findSlideJS = `
(() => {
  const el = document.querySelector('.slide-wrapper') || document.querySelector('.slide');
  if (!el) return null;
  const r = el.getBoundingClientRect();
  if (r.width < 50 || r.height < 50) return null;

  let total = 0, current = 0;
  const countEl = document.querySelector('.player-v2-chrome-controls-slide-count');
  const text = (countEl && countEl.textContent) || document.body.innerText || '';
  const m = text.match(/(\d+)\s*\/\s*(\d+)/);
  if (m) { current = parseInt(m[1], 10); total = parseInt(m[2], 10); }

  return { x: r.left, y: r.top, w: r.width, h: r.height, total, current };
})()
`

// hideOverlaysJS strips the player chrome and any popovers/banners that would
// otherwise leak into the screenshots. The slide clip sits above the chrome
// so there's usually no overlap, but Pitch floats a "make a presentation like
// this one" branding popover and the odd menu over the stage, so we hide them
// defensively along with the usual cookie/consent banners.
const hideOverlaysJS = `
(() => {
  const hide = el => el && el.style.setProperty('display', 'none', 'important');
  const selectors = [
    '.player-v2-chrome',
    '.player-branding-popover',
    '.player-burger-menu-panel',
    '.player-menu-popover',
    '.player-options-menu',
    '#onetrust-banner-sdk',
    '#onetrust-consent-sdk',
    '[id*="cookie" i][class*="banner" i]',
    '[class*="cookie" i][class*="banner" i]',
    '[data-testid*="cookie" i]',
    '[data-testid*="consent" i]',
    '#intercom-frame', '.intercom-launcher-frame',
    '#hubspot-messages-iframe-container',
  ];
  for (const sel of selectors) {
    document.querySelectorAll(sel).forEach(hide);
  }

  // Pitch's "Can we log your visit?" consent dialog. It lives in a fixed,
  // dimmed modal container over the stage; hide the whole container (not
  // just the form) so the dim backdrop doesn't tint the slides. We don't
  // click "Agree" — that would opt the user into view tracking.
  document.querySelectorAll('.platform-player-consent-form').forEach(f => {
    hide(f.closest('.platform--modal__container') || f);
  });

  // Consent iframes by src (OneTrust/TrustArc/Cookiebot/etc).
  document.querySelectorAll('iframe').forEach(f => {
    const src = (f.src || '') + ' ' + (f.id || '') + ' ' + (typeof f.className === 'string' ? f.className : '');
    if (/onetrust|trustarc|cookiebot|consent|cookie|gdpr|ccpa/i.test(src)) hide(f);
  });
})()
`

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
		chromedp.Evaluate(hideOverlaysJS, nil),
	); err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}

	det, err := findSlide(bctx)
	if err != nil {
		return nil, err
	}
	if det == nil {
		return nil, fmt.Errorf("could not find slide player on the page")
	}

	// Require a known total; repeated screenshots do not prove end of deck.
	maxSlides := det.Total
	if maxSlides <= 0 {
		return nil, fmt.Errorf("could not determine Pitch slide count; no partial PDF was saved")
	}
	fmt.Fprintf(os.Stderr, "deck has %d slide(s)\n", maxSlides)

	var slides []adapter.Slide
	for i := 1; i <= maxSlides; i++ {
		if i > 1 {
			if err := chromedp.Run(bctx, chromedp.KeyEvent(kb.ArrowRight)); err != nil {
				return nil, fmt.Errorf("advance to slide %d: %w", i, err)
			}
		}
		if err := chromedp.Run(bctx,
			chromedp.Sleep(700*time.Millisecond),
			chromedp.Evaluate(hideOverlaysJS, nil),
		); err != nil {
			return nil, err
		}

		rect, err := waitForSlide(bctx, i, maxSlides)
		if err != nil {
			return nil, err
		}

		shot, err := captureViewportClip(bctx, viewportRect{X: rect.X, Y: rect.Y, W: rect.W, H: rect.H})
		if err != nil {
			return nil, fmt.Errorf("screenshot slide %d: %w", i, err)
		}
		slides = append(slides, adapter.Slide{Data: shot, ContentType: "image/png"})
	}

	if len(slides) == 0 {
		return nil, fmt.Errorf("no slides captured")
	}
	return slides, nil
}

func waitForSlide(ctx context.Context, current, total int) (*slideInfo, error) {
	deadline := time.Now().Add(4 * time.Second)
	for {
		info, err := findSlide(ctx)
		if err != nil {
			return nil, err
		}
		if info != nil && info.Current == current && info.Total == total {
			return info, nil
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("Pitch did not display slide %d of %d; no partial PDF was saved", current, total)
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
			return nil, err
		}
	}
}

// slideInfo is the parsed result of findSlideJS.
type slideInfo struct {
	X, Y, W, H float64
	Total      int `json:"total"`
	Current    int `json:"current"`
}

func findSlide(ctx context.Context) (*slideInfo, error) {
	var info *slideInfo
	if err := chromedp.Run(ctx, chromedp.Evaluate(findSlideJS, &info)); err != nil {
		return nil, fmt.Errorf("find slide: %w", err)
	}
	return info, nil
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
