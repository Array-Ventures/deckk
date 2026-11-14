package papermark

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/holman/deckk/internal/adapter"
)

// Fake React-style form: each change enables submission asynchronously. It
// uses the provider's input names, OTP autocomplete, and agreement checkbox.
const gatedViewer = `<!doctype html><body>
<form id="access"><input id="email" name="email"><input id="password" name="password" type="password">
<!-- optional agreement -->
<button type="submit" disabled>Continue</button></form>
<script>
let saved = {};
function bind() {
 const form=document.querySelector('form');
 const update=()=>setTimeout(()=>{ form.querySelector('[type=submit]').disabled=false; },50);
 form.addEventListener('input',update);
 const agreement=form.querySelector('#agreement');
 if(agreement) agreement.onclick=()=>{ agreement.setAttribute('aria-checked','true'); update(); };
 form.onsubmit=async e=>{
  e.preventDefault();
  for(const el of form.querySelectorAll('input')) saved[el.name]=el.value;
  if(agreement) saved.hasConfirmedAgreement=agreement.getAttribute('aria-checked')==='true';
  const result=await fetch('/api/views',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({...saved,documentVersionId:'fixture-version',linkId:'fixture-link'})});
  const data=await result.json();
  if(data.type==='email-verification') {
   document.body.innerHTML='<form><input autocomplete="one-time-code" name="code"><button type="submit" disabled>Verify</button></form>';
   bind();
  }
 };
}
bind();
</script>`

func TestPapermarkAccessAndDownloads(t *testing.T) {
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 120, 60))); err != nil {
		t.Fatal(err)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "missing-passcode", "agreement-blocked", "missing-code", "rejected-code", "missing-page", "duplicate-page", "corrupt-image", "expired-view", "missing-signed-page"} {
		t.Run(mode, func(t *testing.T) {
			var views, signings, refreshes atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/view/fixture":
					w.Header().Set("Content-Type", "text/html")
					http.SetCookie(w, &http.Cookie{Name: "fixture", Value: "session", Path: "/"})
					page := gatedViewer
					if mode == "agreement-blocked" {
						page = strings.Replace(page, "<!-- optional agreement -->", `<div><button type="button" role="checkbox" id="agreement" aria-checked="false">Agreement</button>Fixture confidentiality terms</div>`, 1)
					}
					fmt.Fprint(w, page)
				case "/api/views":
					views.Add(1)
					var body struct {
						Email, Password, Code string
						HasConfirmedAgreement bool
					}
					json.NewDecoder(r.Body).Decode(&body)
					if body.Email != "viewer@example.test" || body.Password != "fixture-passcode" || body.HasConfirmedAgreement {
						t.Error("access values were not preserved")
					}
					if body.Code == "" {
						json.NewEncoder(w).Encode(map[string]string{"type": "email-verification"})
						return
					}
					if body.Code != "123456" {
						w.WriteHeader(http.StatusForbidden)
						fmt.Fprint(w, `{"message":"invalid code"}`)
						return
					}
					pages := make([]map[string]any, 18)
					for i := range pages {
						n := i + 1
						pages[i] = map[string]any{"pageNumber": n, "file": nil}
						if n == 1 {
							pages[i]["file"] = server.URL + "/expired"
						}
						if mode == "missing-page" && n == 5 {
							pages[i]["pageNumber"] = 19
						}
						if mode == "duplicate-page" && n == 5 {
							pages[i]["pageNumber"] = 4
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"viewId": "fixture-view", "pages": pages})
				case "/api/views/pages":
					signings.Add(1)
					if _, err := r.Cookie("fixture"); err != nil {
						t.Error("signing request lost browser session")
					}
					var body struct {
						ViewID, DocumentVersionID string
						PageNumbers               []int
					}
					json.NewDecoder(r.Body).Decode(&body)
					if body.ViewID != "fixture-view" || body.DocumentVersionID != "fixture-version" || len(body.PageNumbers) > 15 {
						t.Errorf("invalid signing request: %+v", body)
					}
					if mode == "expired-view" {
						w.WriteHeader(http.StatusUnauthorized)
						fmt.Fprint(w, `{"message":"expired"}`)
						return
					}
					pages := []map[string]any{}
					for _, n := range body.PageNumbers {
						if mode == "missing-signed-page" && n == 5 {
							continue
						}
						pages = append(pages, map[string]any{"pageNumber": n, "file": server.URL + "/image"})
						if n == 1 {
							refreshes.Add(1)
						}
					}
					json.NewEncoder(w).Encode(map[string]any{"pages": pages})
				case "/expired":
					w.WriteHeader(http.StatusForbidden)
				case "/image":
					if r.Header.Get("Cookie") != "" {
						t.Error("forwarded session cookies to image storage")
					}
					if mode == "corrupt-image" {
						fmt.Fprint(w, "invalid")
						return
					}
					w.Header().Set("Content-Type", "image/png")
					w.Write(img.Bytes())
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			ctx, closeTab := chromedp.NewContext(browser)
			defer closeTab()
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			calls := make(chan apiCall, 4)
			listenForViewsAPI(ctx, calls)
			if err := chromedp.Run(ctx, network.Enable(), chromedp.Navigate(server.URL+"/view/fixture")); err != nil {
				t.Fatal(err)
			}
			opts := adapter.Options{Email: "viewer@example.test", Passcode: "fixture-passcode", RequestVerificationCode: func(context.Context) (string, error) { return "123456", nil }}
			switch mode {
			case "missing-passcode":
				opts.Passcode = ""
			case "missing-code":
				opts.RequestVerificationCode = nil
			case "rejected-code":
				opts.VerificationCode = "654321"
			}
			slides, err := fetchSlides(ctx, calls, opts)
			if mode == "success" {
				if err != nil || len(slides) != 18 {
					t.Fatalf("got %d pages, %v", len(slides), err)
				}
				if views.Load() != 2 || signings.Load() != 3 || refreshes.Load() != 1 {
					t.Fatalf("requests: views=%d signing=%d refreshed=%d", views.Load(), signings.Load(), refreshes.Load())
				}
				for _, slide := range slides {
					if !bytes.Equal(slide.Data, img.Bytes()) {
						t.Error("original image changed")
					}
				}
			} else {
				if err == nil || len(slides) > 0 {
					t.Fatalf("accepted incomplete or gated deck: pages=%d err=%v", len(slides), err)
				}
				if strings.Contains(err.Error(), "fixture-passcode") {
					t.Fatal("error exposed passcode")
				}
				if mode == "agreement-blocked" && !strings.Contains(err.Error(), "Fixture confidentiality terms") {
					t.Errorf("missing agreement text: %v", err)
				}
				if (mode == "missing-passcode" || mode == "agreement-blocked") && views.Load() != 0 {
					t.Error("submitted without all required inputs")
				}
			}
		})
	}
}
