package canva

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/holman/deckk/internal/adapter"
)

func TestDelayedBackgroundImage(t *testing.T) {
	green := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			green.Set(x, y, color.RGBA{0, 128, 0, 255})
		}
	}
	var greenPNG bytes.Buffer
	if err := png.Encode(&greenPNG, green); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/slow.png" {
			time.Sleep(1600 * time.Millisecond)
			w.Header().Set("Content-Type", "image/png")
			w.Write(greenPNG.Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<div data-page-id="0" style="width:640px;height:360px;background:blue"></div><button>1 / 2</button><script>document.onkeydown=e=>{if(e.key==='ArrowRight'){document.querySelector('[data-page-id]').dataset.pageId='1';document.querySelector('button').textContent='2 / 2';document.querySelector('[data-page-id]').style.backgroundImage='url(/slow.png)'}}</script>`)
	}))
	defer s.Close()
	ctx, c := context.WithTimeout(context.Background(), 15*time.Second)
	defer c()
	slides, err := (&Adapter{}).Fetch(ctx, s.URL, adapter.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if len(slides) != 2 {
		t.Fatalf("pages=%d", len(slides))
	}
	if bytes.Equal(slides[0].Data, slides[1].Data) {
		t.Fatal("successfully exported page 1 twice; second page content had not rendered")
	}
	img, err := png.Decode(bytes.NewReader(slides[1].Data))
	if err != nil {
		t.Fatal(err)
	}
	center := color.RGBAModel.Convert(img.At(img.Bounds().Dx()/2, img.Bounds().Dy()/2)).(color.RGBA)
	if center != (color.RGBA{0, 128, 0, 255}) {
		t.Fatalf("captured placeholder instead of loaded image: %v", center)
	}
}
func TestChangingPageTotal(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<div data-page-id="0" style="width:640px;height:360px;background:blue"></div><button>1 / 2</button><script>document.onkeydown=e=>{if(e.key==='ArrowRight'){document.querySelector('[data-page-id]').dataset.pageId='1';document.querySelector('button').textContent='2 / 3';document.querySelector('[data-page-id]').style.background='green'}}</script>`)
	}))
	defer s.Close()
	ctx, c := context.WithTimeout(context.Background(), 15*time.Second)
	defer c()
	slides, err := (&Adapter{}).Fetch(ctx, s.URL, adapter.Options{})
	if err == nil {
		t.Fatalf("export succeeded with %d slides although viewer reports 3", len(slides))
	}
}
