package papermark

import (
	"context"
	"encoding/json"
	"fmt"
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

const customAccessFixture = `<!doctype html><form>
<input name="name" id="name"><input name="email" id="email" type="email"><input name="password" id="password" type="password">
<input name="company" required><textarea name="notes" required></textarea><input type="url" name="website" required>
<!-- additional controls --><button type="submit" disabled>Continue</button></form>
<script>const form=document.querySelector('form');form.oninput=()=>setTimeout(()=>{form.querySelector('[type=submit]').disabled=false},20);
form.onsubmit=e=>{e.preventDefault();fetch('/api/views',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({...Object.fromEntries(new FormData(form)),documentVersionId:'version',linkId:'link'})})};</script>`

func TestCustomAccessFields(t *testing.T) {
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"success", "custom-only", "missing-name", "missing-field", "unknown-field", "checkbox", "signing"} {
		t.Run(mode, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/api/views" {
					posts.Add(1)
					var values map[string]string
					json.NewDecoder(r.Body).Decode(&values)
					if values["company"] != "Example Company" || values["notes"] != "A quoted \"value\"\nand a second line" || values["website"] != "https://example.test" {
						t.Error("custom values were changed")
					}
					if mode != "custom-only" && (values["name"] != "Example Visitor" || values["email"] != "viewer@example.test" || values["password"] != "fixture-passcode") {
						t.Error("visitor credentials were changed")
					}
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprint(w, `{"viewId":"view","fileType":"pdf","file":"https://example.test/file.pdf"}`)
					return
				}
				page := customAccessFixture
				if mode == "checkbox" {
					page = strings.Replace(page, "<!-- additional controls -->", `<button type="button" role="checkbox">A required checkbox</button>`, 1)
				}
				if mode == "signing" {
					page = strings.Replace(page, "<!-- additional controls -->", `<button type="button">Open signing</button>`, 1)
				}
				if mode == "custom-only" {
					page = strings.Replace(page, `<input name="name" id="name"><input name="email" id="email" type="email"><input name="password" id="password" type="password">`, "", 1)
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, page)
			}))
			defer server.Close()
			ctx, closeTab := chromedp.NewContext(browser)
			defer closeTab()
			ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			calls := make(chan apiCall, 4)
			listenForViewsAPI(ctx, calls)
			if err := chromedp.Run(ctx, network.Enable(), chromedp.Navigate(server.URL)); err != nil {
				t.Fatal(err)
			}
			opts := adapter.Options{Name: "Example Visitor", Email: "viewer@example.test", Passcode: "fixture-passcode", Fields: map[string]string{"company": "Example Company", "notes": "A quoted \"value\"\nand a second line", "website": "https://example.test"}}
			switch mode {
			case "custom-only":
				opts.Name = ""
				opts.Email = ""
				opts.Passcode = ""
			case "missing-name":
				opts.Name = ""
			case "missing-field":
				delete(opts.Fields, "company")
			case "unknown-field":
				opts.Fields["typo"] = "value"
			}
			_, err := waitForDeck(ctx, calls, opts)
			if mode == "success" || mode == "custom-only" {
				if err != nil || posts.Load() != 1 {
					t.Fatalf("access failed: posts=%d, err=%v", posts.Load(), err)
				}
			} else if err == nil || posts.Load() != 0 {
				t.Fatalf("unexpected submission: posts=%d, err=%v", posts.Load(), err)
			}
		})
	}
}

func TestAccessStatusMessages(t *testing.T) {
	for status, word := range map[int]string{401: "credentials", 403: "credentials", 404: "expired", 410: "revoked", 429: "rate limit", 500: "failed"} {
		if !strings.Contains(accessError(status).Error(), word) {
			t.Errorf("missing explanation for HTTP %d", status)
		}
	}
}
