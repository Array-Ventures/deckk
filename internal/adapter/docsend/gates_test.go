package docsend

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/holman/deckk/internal/adapter"
)

// Real browser tests against local access forms, including HTTP redirects
// and session cookies. No traffic to DocSend or other external sites.
func TestAccessGates(t *testing.T) {
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer cancelAlloc()
	browser, cancelBrowser := chromedp.NewContext(alloc)
	defer cancelBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}

	const passcode = `test-"quote'\\passcode`
	tests := []struct {
		name    string
		start   string
		opts    adapter.Options
		wantErr string
		posts   int32
	}{
		{"email password OTP", "/", adapter.Options{Email: "viewer@example.test", Passcode: passcode, RequestVerificationCode: func(ctx context.Context) (string, error) { return "654321", nil }}, "", 2},
		{"separate email and password", "/email", adapter.Options{Email: "viewer@example.test", Passcode: passcode, VerificationCode: "654321"}, "", 3},
		{"missing email", "/", adapter.Options{Passcode: passcode}, "requires an email", 0},
		{"missing password", "/", adapter.Options{Email: "viewer@example.test"}, "requires a passcode", 0},
		{"wrong password", "/", adapter.Options{Email: "viewer@example.test", Passcode: "incorrect"}, "rejected access", 1},
		{"NDA stops for manual handling", "/nda", adapter.Options{}, "Confidential test agreement", 0},
		{"signature stops for manual handling", "/signature", adapter.Options{Email: "viewer@example.test", Passcode: passcode}, "requires an electronic signature", 0},
		{"OTP required", "/verify", adapter.Options{}, "requires an emailed verification code", 0},
		{"OTP from flag", "/verify", adapter.Options{VerificationCode: "654321"}, "", 1},
		{"wrong OTP", "/verify", adapter.Options{VerificationCode: "000000"}, "rejected access", 1},
		{"OTP callback fails", "/verify", adapter.Options{RequestVerificationCode: func(ctx context.Context) (string, error) { return "", fmt.Errorf("input unavailable") }}, "input unavailable", 0},
		{"feedback is not a gate", "/viewer", adapter.Options{Email: "viewer@example.test", Passcode: passcode}, "", 0},
		{"unsupported required field", "/extra", adapter.Options{Email: "viewer@example.test"}, "requires additional or valid input", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				if r.Method == http.MethodPost {
					posts.Add(1)
					switch r.URL.Path {
					case "/email":
						if r.FormValue("email") != "viewer@example.test" {
							t.Error("missing email on separate form")
						}
						http.Redirect(w, r, "/password", http.StatusSeeOther)
					case "/password":
						if r.FormValue("passcode") != passcode {
							t.Error("missing passcode on separate form")
						}
						http.Redirect(w, r, "/delayed", http.StatusSeeOther)
					case "/":
						if r.FormValue("link_auth_form[email]") != "viewer@example.test" || r.FormValue("link_auth_form[passcode]") != passcode {
							fmt.Fprint(w, `<div id="prompt"><div class="alert_content">Invalid credentials</div></div>`)
							return
						}
						http.SetCookie(w, &http.Cookie{Name: "test-session", Value: "ok", Path: "/"})
						http.Redirect(w, r, "/delayed", http.StatusSeeOther)
					case "/nda":
						t.Error("submitted an agreement despite manual-only policy")
						w.WriteHeader(http.StatusBadRequest)
					case "/verify":
						if tt.start == "/" {
							if cookie, err := r.Cookie("test-session"); err != nil || cookie.Value != "ok" {
								t.Error("browser lost session between access gates")
							}
						}
						if r.FormValue("link_auth_form[verification_code]") != "654321" {
							fmt.Fprint(w, `<div id="prompt"><div class="error-message">Invalid code</div></div>`)
							return
						}
						http.Redirect(w, r, "/viewer", http.StatusSeeOther)
					default:
						t.Error("submitted an unrelated form")
					}
					return
				}
				switch r.URL.Path {
				case "/email":
					fmt.Fprint(w, `<form method="post"><input type="email" name="email"><button>Continue</button></form>`)
				case "/password":
					fmt.Fprint(w, `<form method="post"><input type="password" name="passcode"><button>Continue</button></form>`)
				case "/extra":
					fmt.Fprint(w, `<form method="post"><input type="email" name="email"><input name="company" required><button>Continue</button></form>`)
				case "/":
					fmt.Fprint(w, `<form method="post"><input name="link_auth_form[email]" placeholder="Email address"><input type="password" name="link_auth_form[passcode]"><button>Continue</button></form>`)
				case "/nda":
					fmt.Fprint(w, `<div class="nda-agreement"><div class="nda-agreement__content">Confidential test agreement</div><form method="post"><input type="checkbox" required><button>I agree</button></form></div>`)
				case "/signature":
					fmt.Fprint(w, `<form method="post"><input name="link_auth_form[name]"><input type="email" name="email"><input type="password" name="passcode"></form><p>eSignature Required</p><button type="button">Start Signing</button><form class="js-signature-modal-form" hidden></form>`)
				case "/verify":
					fmt.Fprint(w, `<form method="post"><input name="link_auth_form[verification_code]" autocomplete="one-time-code"><button>Verify</button></form>`)
				case "/delayed":
					fmt.Fprint(w, `<body><script>setTimeout(() => { document.body.innerHTML = '<form method="post" action="/verify"><input name="link_auth_form[verification_code]"><button>Verify</button></form>'; }, 400);</script>`)
				case "/viewer":
					fmt.Fprint(w, `<img class="page-view" width="600" height="400" src="data:image/svg+xml,`+html.EscapeString(`<svg xmlns="http://www.w3.org/2000/svg" width="600" height="400"><rect width="600" height="400" fill="blue"/></svg>`)+`"><form id="feedback"><input type="email"><input type="password"><button>Continue</button></form>`)
				}
			}))
			defer server.Close()
			ctx, cancelTab := chromedp.NewContext(browser)
			defer cancelTab()
			ctx, cancelTimeout := context.WithTimeout(ctx, 20*time.Second)
			defer cancelTimeout()
			if err := chromedp.Run(ctx, chromedp.Navigate(server.URL+tt.start)); err != nil {
				t.Fatal(err)
			}
			err := handleAccessGates(ctx, tt.opts)
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("got %v; want error containing %q", err, tt.wantErr)
			}
			if err != nil && strings.Contains(err.Error(), passcode) {
				t.Fatal("error exposed passcode")
			}
			if got := posts.Load(); got != tt.posts {
				t.Errorf("submitted %d forms; want %d", got, tt.posts)
			}
		})
	}
}
