package screenshot

import (
	"bytes"
	"context"
	"fmt"
	"github.com/chromedp/chromedp"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSVGImageResourceReadiness(t *testing.T) {
	green := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			green.Set(x, y, color.RGBA{0, 128, 0, 255})
		}
	}
	var payload bytes.Buffer
	if err := png.Encode(&payload, green); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow.png" {
			time.Sleep(1600 * time.Millisecond)
			w.Header().Set("Content-Type", "image/png")
			w.Write(payload.Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<style>body{margin:0}</style><svg width="640" height="360"><rect width="640" height="360" fill="blue"/><image width="640" height="360"/></svg>`)
	}))
	defer server.Close()
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	ctx, closeTab := chromedp.NewContext(alloc)
	defer closeTab()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL), chromedp.Evaluate(`document.querySelector('image').setAttribute('href','/slow.png')`, nil)); err != nil {
		t.Fatal(err)
	}
	shot, err := CaptureSettled(ctx, Rect{W: 640, H: 360}, nil)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(shot))
	if err != nil {
		t.Fatal(err)
	}
	center := color.RGBAModel.Convert(img.At(640, 360)).(color.RGBA)
	if center != (color.RGBA{0, 128, 0, 255}) {
		t.Fatalf("pending SVG image exported as placeholder: %v", center)
	}
}
