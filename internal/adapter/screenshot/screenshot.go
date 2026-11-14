// Package screenshot captures rendered slides after their visible resources
// and pixels have settled. A page counter alone is not a render-ready signal.
package screenshot

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

type Rect struct{ X, Y, W, H float64 }

const (
	poll           = 300 * time.Millisecond
	unchangedGrace = 3 * time.Second
	maxSettle      = 12 * time.Second
)

// CaptureSettled waits for visible images and fonts, then three equal frames.
// An unchanged previous slide gets a grace period: navigation can update its
// index before replacing the old content. Genuine duplicate slides are kept.
// Unfinished resources or animations fail rather than exporting placeholders.
func CaptureSettled(ctx context.Context, r Rect, previous []byte) ([]byte, error) {
	started := time.Now()
	previousHash := sha256.Sum256(previous)
	var lastHash [32]byte
	repeats := 0
	for {
		var ready bool
		js := fmt.Sprintf(`(() => {
  if (document.fonts && document.fonts.status !== 'loaded') return false;
  const intersects = el => {
    const b = el.getBoundingClientRect(), s = getComputedStyle(el);
    return s.visibility !== 'hidden' && s.display !== 'none' && b.width > 0 && b.height > 0 &&
        b.right > %f && b.bottom > %f && b.left < %f && b.top < %f;
  };
  if (document.getAnimations().some(a => a.playState === 'running' &&
      a.effect && a.effect.target instanceof Element && intersects(a.effect.target))) return false;
  if (!Array.from(document.images).filter(intersects).every(img => img.complete && img.naturalWidth > 0)) return false;
  // Background images do not appear in document.images. Image probes share
  // the browser's in-flight resource/cache and expose completion and failure.
  const probes = window.__deckkSlideImages || (window.__deckkSlideImages = new Map());
  const loaded = url => {
    let img = probes.get(url);
    if (!img) { img = new Image(); probes.set(url, img); img.src = url; }
    return img.complete && img.naturalWidth > 0;
  };
  let ready = true;
  for (const el of document.querySelectorAll('svg image')) {
    if (!intersects(el)) continue;
    const href = el.href.baseVal;
    if (href && !loaded(new URL(href, el.baseURI).href)) ready = false;
  }
  for (const el of document.querySelectorAll('*')) {
    if (!intersects(el)) continue;
    const bg = getComputedStyle(el).backgroundImage;
    for (const match of bg.matchAll(/url\((?:"([^"]*)"|'([^']*)'|([^)]*))\)/g)) {
      const url = match[1] || match[2] || match[3].trim();
      if (!loaded(url)) ready = false;
    }
  }
  return ready;
})()`, r.X, r.Y, r.X+r.W, r.Y+r.H)
		if err := chromedp.Run(ctx, chromedp.Evaluate(js, &ready)); err != nil {
			return nil, err
		}
		if ready {
			shot, err := capture(ctx, r)
			if err != nil {
				return nil, err
			}
			h := sha256.Sum256(shot)
			if h == lastHash {
				repeats++
			} else {
				repeats = 1
				lastHash = h
			}
			oldFrame := len(previous) > 0 && h == previousHash && time.Since(started) < unchangedGrace
			if repeats >= 3 && !oldFrame {
				return shot, nil
			}
			if time.Since(started) >= maxSettle {
				return nil, fmt.Errorf("slide rendering did not settle")
			}
		} else {
			repeats = 0
			if time.Since(started) >= maxSettle {
				return nil, fmt.Errorf("slide images or fonts did not finish loading")
			}
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(poll)); err != nil {
			return nil, err
		}
	}
}

func capture(ctx context.Context, r Rect) ([]byte, error) {
	var buf []byte
	err := chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		var err error
		buf, err = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).
			WithClip(&page.Viewport{X: r.X, Y: r.Y, Width: r.W, Height: r.H, Scale: 2}).Do(ctx)
		return err
	}))
	return buf, err
}
