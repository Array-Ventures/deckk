package docsend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
)

func TestOriginalSlideDownload(t *testing.T) {
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 80, 40))); err != nil {
		t.Fatal(err)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"directImageUrl", "imageUrl", "image_url", "missing-page", "corrupt", "unsupported", "forbidden"} {
		t.Run(mode, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/view/deck":
					http.SetCookie(w, &http.Cookie{Name: "auth", Value: "fixture", Path: "/"})
					fmt.Fprint(w, `<div data-page-num="1"></div><div data-page-num="3"></div>`)
				case strings.Contains(r.URL.Path, "/page_data/"):
					if c, err := r.Cookie("auth"); err != nil || c.Value != "fixture" {
						t.Error("metadata request lost browser session")
					}
					n, _ := strconv.Atoi(r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
					if mode == "unsupported" || (mode == "missing-page" && n == 2) {
						http.NotFound(w, r)
						return
					}
					if mode == "forbidden" {
						w.WriteHeader(http.StatusForbidden)
						return
					}
					key := mode
					if mode == "missing-page" || mode == "corrupt" {
						key = "directImageUrl"
					}
					json.NewEncoder(w).Encode(map[string]string{key: server.URL + "/image/" + strconv.Itoa(n)})
				case strings.HasPrefix(r.URL.Path, "/image/"):
					if r.Header.Get("Cookie") != "" {
						t.Error("forwarded browser cookies to image storage")
					}
					if mode == "corrupt" {
						fmt.Fprint(w, "not an image")
						return
					}
					w.Write(img.Bytes())
				}
			}))
			defer server.Close()
			ctx, closeTab := chromedp.NewContext(browser)
			defer closeTab()
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := chromedp.Run(ctx, chromedp.Navigate(server.URL+"/view/deck")); err != nil {
				t.Fatal(err)
			}
			total, err := detectTotalSlides(ctx)
			if err != nil || total != 3 {
				t.Fatalf("metadata count: %d, %v", total, err)
			}
			slides, supported, err := fetchOriginalSlides(ctx, total)
			switch mode {
			case "unsupported":
				if supported || err != nil || len(slides) > 0 {
					t.Fatalf("unexpected fallback: %v, %v", supported, err)
				}
			case "missing-page", "corrupt", "forbidden":
				if !supported || err == nil || len(slides) > 0 {
					t.Fatalf("incomplete deck accepted: %d, %v, %v", len(slides), supported, err)
				}
			default:
				if err != nil || !supported || len(slides) != 3 {
					t.Fatalf("download: %d, %v, %v", len(slides), supported, err)
				}
				for _, slide := range slides {
					if !bytes.Equal(slide.Data, img.Bytes()) {
						t.Error("original image bytes changed")
					}
				}
			}
		})
	}
}
