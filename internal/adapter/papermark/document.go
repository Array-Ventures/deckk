package papermark

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/holman/deckk/internal/adapter"
	"github.com/holman/deckk/internal/adapter/imagefetch"
	"github.com/holman/deckk/internal/adapter/nativefile"
)

func fetchDocument(ctx context.Context, calls <-chan apiCall, opts adapter.Options) (adapter.Document, error) {
	call, err := waitForDeck(ctx, calls, opts)
	if err != nil {
		return adapter.Document{}, err
	}
	// The view response arrives just before React mounts the native control.
	if err := chromedp.Run(ctx, chromedp.Sleep(300*time.Millisecond)); err != nil {
		return adapter.Document{}, err
	}
	data, found, err := nativefile.Try(ctx)
	if err != nil {
		return adapter.Document{}, err
	}
	if found && nativefile.IsPDF(data) {
		if err := nativefile.ValidatePDF(data); err != nil {
			return adapter.Document{}, err
		}
		fmt.Fprintln(os.Stderr, "downloaded Papermark's native PDF")
		return adapter.Document{PDF: data}, nil
	}
	if config := strings.TrimSpace(string(call.Resp.WatermarkConfig)); config != "" && config != "null" {
		return adapter.Document{}, fmt.Errorf("Papermark requires a watermark; use its native PDF download or complete the download manually to preserve it")
	}
	if len(call.Resp.Pages) > 0 {
		slides, err := processSlides(ctx, call, opts)
		return adapter.Document{Slides: slides}, err
	}
	switch call.Resp.FileType {
	case "pdf":
		data, err := downloadPDF(ctx, call.Resp.File)
		return adapter.Document{PDF: data}, err
	case "image":
		slide, err := imagefetch.Download(ctx, &http.Client{Timeout: 60 * time.Second}, call.Resp.File)
		if err != nil {
			return adapter.Document{}, err
		}
		return adapter.Document{Slides: []adapter.Slide{slide}}, nil
	default:
		return adapter.Document{}, fmt.Errorf("Papermark file type %q cannot be exported as a PDF by this tool", call.Resp.FileType)
	}
}

func downloadPDF(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil || (req.URL.Scheme != "https" && req.URL.Scheme != "http") {
		return nil, fmt.Errorf("invalid Papermark file URL")
	}
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("Papermark PDF download failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Papermark PDF download returned HTTP %d", resp.StatusCode)
	}
	const maxBytes = 256 << 20
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil || len(data) > maxBytes {
		return nil, fmt.Errorf("Papermark PDF was incomplete or exceeds 256 MiB")
	}
	if err := nativefile.ValidatePDF(data); err != nil {
		return nil, err
	}
	return data, nil
}
