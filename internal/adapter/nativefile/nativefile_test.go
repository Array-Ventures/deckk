package nativefile

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/go-pdf/fpdf"
)

func TestNativeDownloadKeepsOriginalBytesAndSession(t *testing.T) {
	var original bytes.Buffer
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(50, 10, "Native selectable text")
	if err := pdf.Output(&original); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/native.pdf" {
			cookie, err := r.Cookie("fixture-session")
			if err != nil || cookie.Value != "authorized" {
				t.Error("native download lost browser session")
				w.WriteHeader(403)
				return
			}
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", `attachment; filename="fixture.pdf"`)
			w.Write(original.Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		http.SetCookie(w, &http.Cookie{Name: "fixture-session", Value: "authorized", Path: "/", HttpOnly: true})
		if r.URL.Path == "/menu" {
			fmt.Fprint(w, `<button aria-label="More options" onclick="document.querySelector('section').hidden=false">…</button><section hidden><a href="/native.pdf" data-original-title="Download presentation (.pdf)">↓</a></section>`)
			return
		}
		fmt.Fprint(w, `<a href="/native.pdf" title="Download document" download>Download</a>`)
	}))
	defer server.Close()
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 15*time.Second)
	defer timeout()
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL)); err != nil {
		t.Fatal(err)
	}
	data, found, err := Try(ctx)
	if err != nil || !found || !bytes.Equal(data, original.Bytes()) {
		t.Fatalf("native download: found=%v, err=%v, bytes=%d", found, err, len(data))
	}
	if err := ValidatePDF(data); err != nil {
		t.Fatal(err)
	}
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL+"/menu")); err != nil {
		t.Fatal(err)
	}
	data, found, err = Try(ctx)
	if err != nil || !found || !bytes.Equal(data, original.Bytes()) {
		t.Fatalf("menu download: found=%v, err=%v", found, err)
	}
}

func TestDownloadAbsenceAndPDFValidation(t *testing.T) {
	ctx, cancel := chromedp.NewContext(context.Background())
	defer cancel()
	ctx, timeout := context.WithTimeout(ctx, 5*time.Second)
	defer timeout()
	if err := chromedp.Run(ctx, chromedp.Navigate("about:blank")); err != nil {
		t.Fatal(err)
	}
	if _, found, err := Try(ctx); err != nil || found {
		t.Fatalf("unexpected download control: %v, %v", found, err)
	}
	for _, data := range [][]byte{[]byte("<html>not a PDF</html>"), []byte("%PDF-1.4 truncated body")} {
		if ValidatePDF(data) == nil {
			t.Error("invalid PDF accepted")
		}
	}
}
