package browserskill

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

func TestDocsendInspector(t *testing.T) {
	script, err := os.ReadFile("../../skills/deckk/scripts/docsend_inspect.js")
	if err != nil {
		t.Fatal(err)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	// Canvas stands in for a fully loaded slide without any external traffic.
	slide := `<canvas width="640" height="360"></canvas><script>document.querySelector('canvas').getContext('2d').fillRect(0,0,640,360)</script>`
	tests := []struct {
		name, html, status, gate string
		total                    float64
		download                 bool
	}{
		{"access denied", `<title>ERROR: The request could not be satisfied</title><h1>403 ERROR</h1><p>Request blocked</p>`, "gate", "access-denied", 0, false},
		{"credentials", `<form><input type="email"><input type="password"><button>Continue</button></form>`, "gate", "credentials", 0, false},
		{"OTP", `<form><input autocomplete="one-time-code"><button>Continue</button></form>`, "gate", "verification-code", 0, false},
		{"rejection", `<form><div role="alert">Incorrect password</div><input type="password"><button>Continue</button></form>`, "gate", "rejected", 0, false},
		{"agreement", `<div class="nda-agreement"><button>I agree</button></div>`, "gate", "agreement", 0, false},
		{"signature", `<form class="js-signature-modal-form"><button>Sign</button></form>`, "gate", "agreement", 0, false},
		{"email link", `<h1>Check your inbox</h1>`, "gate", "verification-link", 0, false},
		{"challenge", `<h1>Verify you are human</h1>`, "gate", "challenge", 0, false},
		{"native only", `<a download href="/deck.pdf">Download PDF</a>`, "native-only", "", 0, true},
		{"native priority", slide + `<div data-page-counter>1 / 3</div><button>Download</button>`, "ready", "", 3, true},
		{"counter not mounted images", slide + `<div data-page-counter>1 / 12</div>`, "ready", "", 12, false},
		{"feedback ignored", slide + `<div data-page-counter>1 / 3</div><form id="feedback"><input type="email"></form>`, "ready", "", 3, false},
		{"body fraction is not a counter", slide + `<p>1 / 1</p><button aria-label="Next slide">Next</button>`, "unsupported", "", 0, false},
		{"counter inside slide ignored", `<div data-page-num="1">` + slide + `<span data-page-counter>1 / 1</span></div>`, "unsupported", "", 0, false},
		{"transparent ancestor", `<div style="opacity:0">` + slide + `</div><div data-page-counter>1 / 3</div>`, "loading", "", 3, false},

		{"ancestor clipped", `<div style="height:100px;width:640px;overflow:hidden">` + slide + `</div><div data-page-counter>1 / 3</div>`, "loading", "", 3, false},

		{"image beside chrome", `<img class="page-view" width="640" height="360" src="data:image/svg+xml,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='640'%20height='360'%3E%3Crect%20width='640'%20height='360'%20fill='blue'/%3E%3C/svg%3E"><div data-page-counter>1 / 3</div>`, "ready", "", 3, false},

		{"missing total", slide, "unsupported", "", 0, false},
		{"missing slide", `<div data-page-counter>1 / 3</div>`, "loading", "", 3, false},
		{"mismatched slide", `<div data-page-num="2">` + slide + `</div><div data-page-counter>1 / 3</div>`, "loading", "", 3, false},
		{"oversized total", slide + `<div data-page-counter>1 / 301</div>`, "unsupported", "", 0, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("Inspector submitted a form")
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<style>body{margin:0}</style>`+tc.html)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(browser, 10*time.Second)
			defer cancel()
			var result map[string]interface{}
			err := chromedp.Run(ctx, chromedp.Navigate(server.URL), chromedp.Evaluate(string(script), &result, func(p *runtime.EvaluateParams) *runtime.EvaluateParams { return p.WithAwaitPromise(true) }))
			if err != nil {
				t.Fatal(err)
			}
			if result["status"] != tc.status {
				t.Fatalf("state=%v", result)
			}
			if tc.gate != "" && result["gate"].(map[string]interface{})["kind"] != tc.gate {
				t.Fatalf("gate=%v", result)
			}
			if tc.total > 0 && result["total"] != tc.total {
				t.Fatalf("total=%v", result)
			}
			if tc.download && result["download"] == nil {
				t.Fatal("Native download was not discovered")
			}
		})
	}
}
