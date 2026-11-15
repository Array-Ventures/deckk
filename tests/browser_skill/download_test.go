package browserskill

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	cdpbrowser "github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
)

func TestNativeDownloadLinkPreservesPDF(t *testing.T) {
	python := os.Getenv("DECKK_TEST_PYTHON")
	if python == "" {
		t.Skip("Set DECKK_TEST_PYTHON for native PDF integration")
	}
	directory := t.TempDir()
	original := filepath.Join(directory, "original.pdf")
	if output, err := exec.Command(python, "-c", "from pypdf import PdfWriter;import sys;w=PdfWriter();[w.add_blank_page(640,360) for _ in range(3)];w.write(sys.argv[1])", original).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, output)
	}
	expected, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/native.pdf" {
			requests.Add(1)
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", `attachment; filename="deck.pdf"`)
			w.Write(expected)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, "<h1>Authorized source link</h1>")
	}))
	defer server.Close()
	source, err := os.ReadFile("../../skills/deckk/scripts/download_link.js")
	if err != nil {
		t.Fatal(err)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	ctx, cancel := context.WithTimeout(browser, 15*time.Second)
	defer cancel()
	url, _ := json.Marshal(server.URL + "/native.pdf")
	var result map[string]interface{}
	if err := chromedp.Run(ctx, chromedp.Navigate(server.URL), cdpbrowser.SetDownloadBehavior(cdpbrowser.SetDownloadBehaviorBehaviorAllow).WithDownloadPath(directory), chromedp.Evaluate(fmt.Sprintf("(%s)(%s)", source, url), &result)); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 0 {
		t.Fatal("Preparing a link unexpectedly fetched the document")
	}
	if err := chromedp.Run(ctx, chromedp.Click(result["selector"].(string), chromedp.ByQuery)); err != nil {
		t.Fatal(err)
	}
	downloaded := filepath.Join(directory, "deck.pdf")
	tick := time.NewTicker(20 * time.Millisecond)
	defer tick.Stop()
	for {
		actual, err := os.ReadFile(downloaded)
		if err == nil && bytes.Equal(actual, expected) {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("download did not preserve PDF: %v", ctx.Err())
		case <-tick.C:
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("download requests=%d", requests.Load())
	}
	helper, _ := filepath.Abs("../../skills/deckk/scripts/pdf_files.py")
	if output, err := exec.Command(python, helper, "inspect", downloaded, "--expected-pages", "3").CombinedOutput(); err != nil {
		t.Fatalf("validate: %v %s", err, output)
	}
	for _, unsafe := range []string{"javascript:alert(1)", "https://user:password@example.invalid/deck.pdf", "file:///private/deck.pdf"} {
		argument, _ := json.Marshal(unsafe)
		if err := chromedp.Run(ctx, chromedp.Evaluate(fmt.Sprintf("(%s)(%s)", source, argument), nil)); err == nil {
			t.Fatalf("accepted unsafe download URL: %s", unsafe)
		}
	}
}
