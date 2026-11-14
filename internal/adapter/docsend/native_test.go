package docsend

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-pdf/fpdf"
	"github.com/holman/deckk/internal/adapter"
)

func TestNativePDFTakesPrecedenceOverSlideImages(t *testing.T) {
	var original bytes.Buffer
	pdf := fpdf.New("P", "mm", "A4", "")
	pdf.AddPage()
	pdf.SetFont("Arial", "", 12)
	pdf.Cell(50, 10, "Original selectable text")
	if err := pdf.Output(&original); err != nil {
		t.Fatal(err)
	}
	var imageRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "page_data") {
			imageRequests.Add(1)
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/native.pdf" {
			w.Header().Set("Content-Type", "application/pdf")
			w.Header().Set("Content-Disposition", `attachment; filename="fixture.pdf"`)
			w.Write(original.Bytes())
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<p>1 / 1</p><a href="/native.pdf" download title="Download presentation">Download</a><img class="page-view" width="600" height="400" src="data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='600' height='400'%3E%3Crect width='600' height='400' fill='blue'/%3E%3C/svg%3E">`)
	}))
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	result, err := (&Adapter{}).FetchDocument(ctx, server.URL, adapter.Options{})
	if err != nil || !bytes.Equal(result.PDF, original.Bytes()) || len(result.Slides) > 0 || imageRequests.Load() != 0 {
		t.Fatalf("native path: err=%v, PDF bytes=%d, image requests=%d", err, len(result.PDF), imageRequests.Load())
	}
}
