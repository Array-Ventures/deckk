package canva

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
	"github.com/holman/deckk/internal/adapter"
)

func TestAnonymousViewerCountAndCrop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><style>body{margin:0} .shell{position:absolute;inset:0}
.slide{position:absolute;left:24px;top:124px;width:1200px;height:675px}
footer{position:absolute;top:900px}</style>
<div class="shell"><div class="shell"><div class="shell">
<div data-page-id="0" class="slide"><svg width="1200" height="675"><rect width="1200" height="675" fill="blue"/></svg></div>
<footer><button id="count"> 1 / 21</button><input type="range" aria-label="Zoom" max="2" value="1"></footer>
</div></div></div><script>document.onkeydown=e=>{if(e.key==='ArrowRight'){document.querySelector('#count').textContent='';document.querySelector('[data-page-id]').dataset.pageId='1'}}</script>`)
	}))
	defer server.Close()
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	ctx, closeTab := chromedp.NewContext(alloc)
	defer closeTab()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var rect *struct{ X, Y, W, H float64 }
	var state pageState
	var footerVisible bool
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1600, 1200), chromedp.Navigate(server.URL),
		chromedp.Evaluate(hideOverlaysJS, nil), chromedp.Evaluate(detectPlayerJS, &rect), chromedp.Evaluate(pageStateJS, &state),
		chromedp.Evaluate(`getComputedStyle(document.querySelector('footer')).display !== 'none'`, &footerVisible)); err != nil {
		t.Fatal(err)
	}
	if rect == nil || rect.X != 24 || rect.Y != 124 || rect.W != 1200 || rect.H != 675 {
		t.Fatalf("capture includes viewer chrome: %+v", rect)
	}
	if state.Total != 21 || state.Current != 1 {
		t.Fatalf("wrong counter after hiding footer: %+v", state)
	}
	if !footerVisible {
		t.Fatal("hiding the footer makes Canva unmount its counter")
	}
	if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.ArrowRight)); err != nil {
		t.Fatal(err)
	}
	if err := waitForPage(ctx, 2, 21); err != nil {
		t.Fatal(err)
	}
}

func TestCompleteCaptureAndStuckPage(t *testing.T) {
	for _, mode := range []string{"complete", "stuck", "start-late"} {
		t.Run(mode, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprintf(w, `<!doctype html><style>body{margin:0}[role=presentation]{position:absolute;left:24px;top:80px;width:640px;height:360px}footer{position:absolute;top:900px}</style>
<div role="presentation" data-page-id="0"><svg width="640" height="360"><rect width="640" height="360" fill="blue"/></svg></div>
<footer><button id="count"></button><button aria-label="Present full screen">Present</button></footer>
<script>const mode=%q;
if(mode==='start-late')document.querySelector('[data-page-id]').dataset.pageId='1';
setTimeout(()=>document.querySelector('#count').textContent='1 / 2',5200);
document.onkeydown=e=>{if(e.key==='ArrowRight' && mode!=='stuck'){document.querySelector('#count').textContent='';document.querySelector('[data-page-id]').dataset.pageId='1'}}</script>`, mode)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 18*time.Second)
			defer cancel()
			slides, err := (&Adapter{}).Fetch(ctx, server.URL, adapter.Options{})
			if mode != "complete" {
				if err == nil || !strings.Contains(err.Error(), "stuck") || len(slides) != 0 {
					t.Fatalf("partial deck returned: slides=%d err=%v", len(slides), err)
				}
				return
			}
			if err != nil || len(slides) != 2 {
				t.Fatalf("incomplete capture: slides=%d err=%v", len(slides), err)
			}
			if !bytes.Equal(slides[0].Data, slides[1].Data) {
				t.Fatal("fixture pages should be identical")
			}
			for _, slide := range slides {
				cfg, _, err := image.DecodeConfig(bytes.NewReader(slide.Data))
				if err != nil || cfg.Width != 1280 || cfg.Height != 720 {
					t.Fatalf("wrong slide crop: %+v err=%v", cfg, err)
				}
			}
		})
	}
}

func TestViewerStateEdgeCases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><style>.slide{position:absolute;width:640px;height:360px;left:24px;top:80px}</style>
<div role="slider" aria-label="Zoom" aria-valuenow="2" aria-valuemax="10"></div>
<div data-page-id="99" class="slide" style="left:-100px"></div>
<div data-page-id="0" class="slide"></div><button>1 / 21</button>`)
	}))
	defer server.Close()
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	ctx, closeTab := chromedp.NewContext(alloc)
	defer closeTab()
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	var state pageState
	if err := chromedp.Run(ctx, chromedp.EmulateViewport(1600, 1200), chromedp.Navigate(server.URL+"#2"), chromedp.Evaluate(pageStateJS, &state)); err != nil {
		t.Fatal(err)
	}
	if state.Total != 21 || state.Current != 1 {
		t.Errorf("zoom control or offscreen page mistaken for active viewer state: %+v", state)
	}
	if err := waitForPage(ctx, 2, 21); err == nil {
		t.Fatal("stale URL hash overrode the actual page index")
	}
}
