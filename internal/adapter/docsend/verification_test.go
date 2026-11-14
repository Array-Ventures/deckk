package docsend

import (
	"context"
	"fmt"
	"github.com/chromedp/chromedp"
	"github.com/holman/deckk/internal/adapter"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestVerificationURL(t *testing.T) {
	doc := "https://docsend.com/view/fixture-deck"
	direct := "https://docsend.com/presentation_users/fixture-token?redirect_url=" + url.QueryEscape(doc)
	tracked := "https://track.pstmrk.it/3s/" + url.PathEscape(strings.TrimPrefix(direct, "https://")) + "/fixture/tracking"
	for _, raw := range []string{direct, tracked} {
		got, err := verificationURL(raw, doc)
		if err != nil || got != direct {
			t.Errorf("valid link: got %q, %v", got, err)
		}
	}
	for _, raw := range []string{
		doc,
		"https://evil.test/presentation_users/fixture-token?redirect_url=" + url.QueryEscape(doc),
		"https://evildocsend.com/presentation_users/fixture-token?redirect_url=" + url.QueryEscape(doc),
		"http://docsend.com/presentation_users/fixture-token?redirect_url=" + url.QueryEscape(doc),
		"https://docsend.com/presentation_users/fixture-token?redirect_url=" + url.QueryEscape("https://docsend.com/view/another-deck"),
		"https://docsend.com/presentation_users/fixture-token?redirect_url=" + url.QueryEscape("https://evil.test/view/fixture-deck"),
		"https://track.pstmrk.it/3s/" + url.PathEscape("evil.test/presentation_users/fixture-token") + "/fixture/tracking",
		"https://docsend.com/presentation_users/",
	} {
		_, err := verificationURL(raw, doc)
		if err == nil {
			t.Errorf("accepted invalid verification URL")
		}
		if err != nil && strings.Contains(err.Error(), "fixture-token") {
			t.Error("error leaked verification token")
		}
	}
}

func TestVerificationRetainsSession(t *testing.T) {
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	for _, expired := range []bool{false, true} {
		t.Run(fmt.Sprintf("expired=%v", expired), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html")
				switch r.URL.Path {
				case "/start":
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "pending", Path: "/"})
					fmt.Fprint(w, "Waiting for verification")
				case "/verify":
					if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "pending" {
						t.Error("verification lost original session")
					}
					if expired {
						fmt.Fprint(w, `<div class="alert_content">Verification link expired https://example.test/private-token</div>`)
						return
					}
					http.SetCookie(w, &http.Cookie{Name: "session", Value: "verified", Path: "/"})
					fmt.Fprint(w, `<div class="alert_content">Email successfully verified</div>`)
				case "/document":
					if cookie, err := r.Cookie("session"); err != nil || cookie.Value != "verified" {
						t.Error("document did not receive verified session")
					}
					fmt.Fprint(w, `<img class="page-view" width="600" height="400" src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='600' height='400'%3E%3Crect width='600' height='400' fill='blue'/%3E%3C/svg%3E">`)
				}
			}))
			defer server.Close()
			ctx, closeTab := chromedp.NewContext(browser)
			defer closeTab()
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			if err := chromedp.Run(ctx, chromedp.Navigate(server.URL+"/start")); err != nil {
				t.Fatal(err)
			}
			err := followVerification(ctx, server.URL+"/document", server.URL+"/verify", adapter.Options{})
			if expired {
				if err == nil || !strings.Contains(err.Error(), "expired") || strings.Contains(err.Error(), "private-token") {
					t.Fatalf("expected redacted expiry error; got %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
		})
	}
}
