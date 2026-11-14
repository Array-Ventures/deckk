package docsend

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/holman/deckk/internal/adapter"
	"github.com/holman/deckk/internal/adapter/imagefetch"
)

var errVerificationRequired = errors.New("DocSend denied page access (HTTP 403); if email verification is enabled, supply the unopened email link with --verification-link or use the CLI prompt")

const originalPageJS = `async (number) => {
  const url = new URL(location.href);
  url.pathname = url.pathname.replace(/\/$/, '') + '/page_data/' + number;
  const response = await fetch(url, {credentials: 'same-origin'});
  if (!response.ok) return {status: response.status};
  try {
    const data = await response.json();
    return {status: response.status, url: data.directImageUrl || data.imageUrl || data.image_url || ''};
  } catch (_) { return {status: response.status}; }
}`

// Only an unavailable first endpoint permits screenshot fallback. After original
// extraction begins, a failed page is an incomplete download, never a short PDF.
func fetchOriginalSlides(ctx context.Context, total int) ([]adapter.Slide, bool, error) {
	if total <= 0 {
		return nil, false, nil
	}
	if total > 300 {
		return nil, true, fmt.Errorf("DocSend reports %d pages, exceeding the 300-page limit", total)
	}
	client := &http.Client{Timeout: 60 * time.Second}
	slides := make([]adapter.Slide, 0, total)
	for n := 1; n <= total; n++ {
		var result struct {
			Status int
			URL    string
		}
		num, _ := json.Marshal(n)
		err := chromedp.Run(ctx, chromedp.Evaluate("("+originalPageJS+")("+string(num)+")", &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
		if err != nil {
			return nil, true, fmt.Errorf("read DocSend page %d metadata: %w", n, err)
		}
		if n == 1 && (result.Status == 404 || result.Status == 405 || (result.Status == 200 && result.URL == "")) {
			return nil, false, nil
		}
		if result.Status != 200 || result.URL == "" {
			if result.Status == 403 {
				if n == 1 {
					return nil, true, errVerificationRequired
				}
				return nil, true, fmt.Errorf("DocSend denied page %d/%d (HTTP 403); if email verification is enabled, obtain the unopened email link and retry with --verification-link", n, total)
			}
			return nil, true, fmt.Errorf("incomplete DocSend deck: page %d/%d metadata unavailable (HTTP %d)", n, total, result.Status)
		}
		slide, err := imagefetch.Download(ctx, client, result.URL)
		if err != nil {
			return nil, true, fmt.Errorf("incomplete DocSend deck: page %d/%d: %w", n, total, err)
		}
		slides = append(slides, slide)
	}
	return slides, true, nil
}
