package screenshot

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestUnreadySlideDoesNotReturnScreenshot(t *testing.T) {
	for _, mode := range []string{"image", "background", "svg", "animation"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/missing.png" {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<!doctype html><style>body{margin:0}div,img{width:640px;height:360px}
@keyframes late { from {opacity:1} to {opacity:0} }</style>`)
				switch mode {
				case "image":
					fmt.Fprint(w, `<img src="/missing.png">`)
				case "background":
					fmt.Fprint(w, `<div style="background:blue url(/missing.png)"></div>`)
				case "svg":
					fmt.Fprint(w, `<svg width="640" height="360"><image href="/missing.png" width="640" height="360"/></svg>`)
				case "animation":
					fmt.Fprint(w, `<div style="background:blue;animation:late 1s 3s forwards"></div>`)
				}
			}))
			defer server.Close()
			alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
			defer stop()
			ctx, closeTab := chromedp.NewContext(alloc)
			defer closeTab()
			if err := chromedp.Run(ctx, chromedp.Navigate(server.URL)); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(ctx, 1500*time.Millisecond)
			defer cancel()
			shot, err := CaptureSettled(ctx, Rect{W: 640, H: 360}, nil)
			if !errors.Is(err, context.DeadlineExceeded) || len(shot) > 0 {
				t.Fatalf("unready slide exported: bytes=%d err=%v", len(shot), err)
			}
		})
	}
}
