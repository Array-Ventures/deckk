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
	"github.com/go-pdf/fpdf"
	"github.com/holman/deckk/internal/adapter"
)

func TestAdditionalDocumentFormats(t *testing.T) {
	var original, imageData bytes.Buffer
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(50, 10, "Original text")
	if err := pdf.Output(&original); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&imageData, image.NewRGBA(image.Rect(0, 0, 120, 60))); err != nil {
		t.Fatal(err)
	}
	alloc, stop := chromedp.NewExecAllocator(context.Background(), chromedp.DefaultExecAllocatorOptions[:]...)
	defer stop()
	browser, closeBrowser := chromedp.NewContext(alloc)
	defer closeBrowser()
	if err := chromedp.Run(browser); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"raw-pdf", "raw-image", "native-watermarked-pdf", "watermark-without-download", "truncated-pdf", "unsupported-video", "dataroom-document", "dataroom-index"} {
		t.Run(mode, func(t *testing.T) {
			var files atomic.Int32
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/file":
					files.Add(1)
					if mode == "raw-image" || mode == "dataroom-document" {
						w.Write(imageData.Bytes())
						return
					}
					if mode == "truncated-pdf" {
						fmt.Fprint(w, "%PDF-1.4 truncated content")
						return
					}
					w.Header().Set("Content-Type", "application/pdf")
					w.Header().Set("Content-Disposition", `attachment; filename="fixture.pdf"`)
					w.Write(original.Bytes())
				case "/api/views", "/api/views-dataroom":
					response := map[string]any{"viewId": "view-fixture", "file": server.URL + "/file", "fileType": "pdf"}
					if mode == "raw-image" {
						response["fileType"] = "image"
					}
					if mode == "unsupported-video" {
						response["fileType"] = "video"
					}
					if mode == "native-watermarked-pdf" || mode == "watermark-without-download" {
						response["watermarkConfig"] = map[string]any{"text": "viewer watermark"}
					}
					if mode == "dataroom-document" {
						delete(response, "file")
						response["pages"] = []map[string]any{{"pageNumber": 1, "file": server.URL + "/file"}}
					}
					if mode == "dataroom-index" {
						response = map[string]any{"dataroomViewId": "room-view", "documents": []any{}}
					}
					w.Header().Set("Content-Type", "application/json")
					json.NewEncoder(w).Encode(response)
				default:
					endpoint := "/api/views"
					viewType := ""
					if strings.HasPrefix(mode, "dataroom-") {
						endpoint = "/api/views-dataroom"
						viewType = "DOCUMENT_VIEW"
					}
					if mode == "dataroom-index" {
						viewType = "DATAROOM_VIEW"
					}
					w.Header().Set("Content-Type", "text/html")
					if mode == "native-watermarked-pdf" {
						fmt.Fprint(w, `<a href="/file" title="Download document" download>Download</a>`)
					}
					fmt.Fprintf(w, `<script>fetch(%q,{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify({documentVersionId:'fixture-version',linkId:'fixture-link',viewType:%q})});</script>`, endpoint, viewType)
				}
			}))
			defer server.Close()
			ctx, closeTab := chromedp.NewContext(browser)
			defer closeTab()
			ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			calls := make(chan apiCall, 4)
			listenForViewsAPI(ctx, calls)
			if err := chromedp.Run(ctx, network.Enable(), chromedp.Navigate(server.URL)); err != nil {
				t.Fatal(err)
			}
			result, err := fetchDocument(ctx, calls, adapter.Options{})
			switch mode {
			case "raw-pdf", "native-watermarked-pdf":
				if err != nil || !bytes.Equal(result.PDF, original.Bytes()) || len(result.Slides) > 0 {
					t.Fatalf("original PDF was not preserved: %v", err)
				}
			case "raw-image", "dataroom-document":
				if err != nil || len(result.Slides) != 1 || !bytes.Equal(result.Slides[0].Data, imageData.Bytes()) {
					t.Fatalf("image output: %v", err)
				}
			default:
				if err == nil || len(result.PDF) > 0 || len(result.Slides) > 0 {
					t.Fatalf("unsupported or incomplete document accepted: %v", err)
				}
				if (mode == "watermark-without-download" || mode == "unsupported-video" || mode == "dataroom-index") && files.Load() != 0 {
					t.Error("downloaded a document that should have stopped")
				}
			}
		})
	}
}

func TestPapermarkDomains(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://www.papermark.com/view/example":        true,
		"https://app.papermark.io/view/example":         true,
		"https://papermark.com/view/example/d/document": true,
		"https://www.papermark.com/view/example/embed":  true,
		"https://evilpapermark.com/view/example":        false,
		"https://papermark.com.evil.test/view/example":  false,
		"file://papermark.com/view/example":             false,
	} {
		if got := (&Adapter{}).Matches(raw); got != want {
			t.Errorf("Matches(%q)=%v; want %v", raw, got, want)
		}
	}
}
