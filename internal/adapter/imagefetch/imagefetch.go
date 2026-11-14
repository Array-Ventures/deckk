// Package imagefetch downloads original slide images without forwarding browser cookies.
package imagefetch

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"time"

	"github.com/holman/deckk/internal/adapter"
)

type HTTPError struct{ Status int }

func (e *HTTPError) Error() string { return fmt.Sprintf("image download returned HTTP %d", e.Status) }

// Download validates the actual image bytes before they can enter a successful PDF.
func Download(ctx context.Context, client *http.Client, rawURL string) (adapter.Slide, error) {
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return adapter.Slide{}, fmt.Errorf("invalid image URL")
		}
		if req.URL.Scheme != "https" && req.URL.Scheme != "http" {
			return adapter.Slide{}, fmt.Errorf("image URL must use HTTP or HTTPS")
		}
		resp, err := client.Do(req)
		if err != nil {
			// The URL can contain a bearer token; do not include transport errors.
			if ctx.Err() != nil {
				return adapter.Slide{}, ctx.Err()
			}
			return adapter.Slide{}, fmt.Errorf("image download failed")
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			if attempt < 2 && resp.StatusCode >= 500 {
				select {
				case <-ctx.Done():
					return adapter.Slide{}, ctx.Err()
				case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
					continue
				}
			}
			return adapter.Slide{}, &HTTPError{Status: resp.StatusCode}
		}
		const maxBytes = 128 << 20
		data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
		resp.Body.Close()
		if err != nil || len(data) > maxBytes {
			return adapter.Slide{}, fmt.Errorf("could not read a complete slide image within the size limit")
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || (format != "png" && format != "jpeg") {
			return adapter.Slide{}, fmt.Errorf("slide is not a valid PNG or JPEG image")
		}
		if _, _, err := image.Decode(bytes.NewReader(data)); err != nil {
			return adapter.Slide{}, fmt.Errorf("slide image is truncated or corrupt")
		}
		return adapter.Slide{Data: data, ContentType: "image/" + format}, nil
	}
	return adapter.Slide{}, fmt.Errorf("image download failed")
}
