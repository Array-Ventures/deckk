package docsend

import (
	"context"
	"fmt"
	"github.com/chromedp/chromedp"
	"github.com/holman/deckk/internal/adapter"
	"net/url"
	"strings"
	"time"
)

// verificationURL unwraps Postmark locally. Neither the tracking service nor
// unrelated hosts receive a request, and the email link must name this deck.
func verificationURL(raw, document string) (string, error) {
	invalid := func() (string, error) {
		return "", fmt.Errorf("expected an unopened DocSend verification link for this document")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.User != nil {
		return invalid()
	}
	if strings.EqualFold(u.Host, "track.pstmrk.it") {
		parts := strings.Split(u.EscapedPath(), "/")
		if len(parts) < 4 || parts[1] != "3s" {
			return invalid()
		}
		decoded, err := url.PathUnescape(parts[2])
		if err != nil {
			return invalid()
		}
		if !strings.HasPrefix(decoded, "https://") {
			decoded = "https://" + decoded
		}
		u, err = url.Parse(decoded)
		if err != nil {
			return invalid()
		}
	}
	if u.Scheme != "https" || u.User != nil || u.Port() != "" || !isDocSendHost(u.Hostname()) || !strings.HasPrefix(u.Path, "/presentation_users/") || strings.TrimPrefix(u.Path, "/presentation_users/") == "" {
		return invalid()
	}
	target, err := url.Parse(u.Query().Get("redirect_url"))
	if err != nil {
		return invalid()
	}
	doc, err := url.Parse(document)
	if err != nil || target.Scheme != "https" || target.User != nil || target.Host != doc.Host || target.Path != doc.Path || !strings.HasPrefix(target.Path, "/view/") {
		return invalid()
	}
	return u.String(), nil
}

// Keep the original browser context, open the email link there, then return to
// the requested document. Some verification pages only notify an opener tab.
func completeVerification(ctx context.Context, document, link string, opts adapter.Options) error {
	u, err := verificationURL(link, document)
	if err != nil {
		return err
	}
	return followVerification(ctx, document, u, opts)
}

func followVerification(ctx context.Context, document, u string, opts adapter.Options) error {
	if err := chromedp.Run(ctx, chromedp.Navigate(u), chromedp.Sleep(time.Second)); err != nil {
		return fmt.Errorf("could not open DocSend verification link")
	}
	var gate accessGate
	if err := chromedp.Run(ctx, chromedp.Evaluate(detectAccessGateJS, &gate)); err != nil {
		return err
	}
	if gate.Kind == "rejected" {
		return fmt.Errorf("DocSend rejected the verification link: %s", safeGateText(gate.Text))
	}
	if err := chromedp.Run(ctx, chromedp.Navigate(document), chromedp.Sleep(2*time.Second)); err != nil {
		return fmt.Errorf("could not return to the document after verification")
	}
	return handleAccessGates(ctx, opts)
}

func isDocSendHost(host string) bool {
	host = strings.ToLower(host)
	return host == "docsend.com" || strings.HasSuffix(host, ".docsend.com") || host == "docsend.dropbox.com"
}
