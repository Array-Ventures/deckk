// Package nativefile captures a provider's own browser download in a private
// temporary directory. Browser cookies stay inside the existing session.
package nativefile

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

// ButtonJS returns the matching visible download control, without clicking.
// Attribute labels also cover icon-only buttons. No share/edit controls match.
const ButtonJS = `() => {
 const visible=el=>el.getClientRects().length>0&&getComputedStyle(el).visibility!=='hidden'&&!el.disabled&&el.getAttribute('aria-disabled')!=='true';
 const matches=text=>/^download(?:\s+(?:this\s+)?(?:pdf|document|file|presentation|deck|now|original)\b.*)?$/i.test((text||'').trim());
 return [...document.querySelectorAll('a,button,[role="menuitem"]')].find(el=>visible(el)&&(
   [el.innerText,el.getAttribute('aria-label'),el.title,el.getAttribute('data-original-title'),el.getAttribute('data-tooltip-content'),document.getElementById(el.getAttribute('aria-describedby'))?.textContent].some(matches)||
   el.querySelector('svg.lucide-download')||
   el.matches('a[download],a#downloadButton,button#downloadButton,a.js-download-document,button.js-download-document')
 )) || null;
}`

func Try(ctx context.Context) ([]byte, bool, error) {
	var found bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(`+ButtonJS+`)() !== null`, &found)); err != nil {
		return nil, false, err
	}
	if !found {
		var opened bool
		if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => {
 const button=[...document.querySelectorAll('button,[role="button"]')].find(el=>el.getClientRects().length&&getComputedStyle(el).visibility!=='hidden'&&/^(more|more options|options)$/i.test(el.getAttribute('aria-label')||el.title||(el.innerText||'').trim()));
 if(!button)return false;button.click();return true;
})()`, &opened)); err != nil {
			return nil, false, err
		}
		if !opened {
			return nil, false, nil
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(150*time.Millisecond), chromedp.Evaluate(`(`+ButtonJS+`)() !== null`, &found)); err != nil {
			return nil, false, err
		}
		if !found {
			if err := chromedp.Run(ctx, chromedp.KeyEvent(kb.Escape)); err != nil {
				return nil, false, err
			}
			return nil, false, nil
		}
	}
	dir, err := os.MkdirTemp("", "deckk-native-")
	if err != nil {
		return nil, true, err
	}
	defer os.RemoveAll(dir)
	listenerCtx, stop := context.WithCancel(ctx)
	defer stop()
	type progress struct {
		guid     string
		state    browser.DownloadProgressState
		tooLarge bool
	}
	events := make(chan progress, 8)
	var mu sync.Mutex
	guid := ""
	finished := false
	defer func() {
		mu.Lock()
		active := guid
		mu.Unlock()
		if !finished && active != "" {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			_ = chromedp.Run(cleanup, browser.CancelDownload(active))
		}
	}()
	observe := func(ev any) {
		mu.Lock()
		defer mu.Unlock()
		switch e := ev.(type) {
		case *browser.EventDownloadWillBegin:
			if guid == "" {
				guid = e.GUID
			}
		case *browser.EventDownloadProgress:
			tooLarge := e.TotalBytes > 256<<20 || e.ReceivedBytes > 256<<20
			if e.GUID == guid && (tooLarge || e.State == browser.DownloadProgressStateCompleted || e.State == browser.DownloadProgressStateCanceled) {
				select {
				case events <- progress{e.GUID, e.State, tooLarge}:
				default:
				}
			}
		}
	}
	// Chrome can deliver these on the target session that configured download
	// behavior, or on the browser session. Subscribe before activating the UI.
	chromedp.ListenTarget(listenerCtx, observe)
	chromedp.ListenBrowser(listenerCtx, observe)
	if err := chromedp.Run(ctx, browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllowAndName).WithDownloadPath(dir).WithEventsEnabled(true)); err != nil {
		return nil, true, fmt.Errorf("enable native browser download: %w", err)
	}
	var clicked bool
	if err := chromedp.Run(ctx, chromedp.Evaluate(`(() => { const el=(`+ButtonJS+`)(); if(!el)return false;el.click();return true;})()`, &clicked)); err != nil {
		return nil, true, fmt.Errorf("could not activate native download")
	}
	if !clicked {
		return nil, true, fmt.Errorf("download control disappeared")
	}
	select {
	case <-ctx.Done():
		return nil, true, ctx.Err()
	case <-time.After(60 * time.Second):
		return nil, true, fmt.Errorf("native download did not complete; it may require additional verification or be unavailable")
	case done := <-events:
		if done.tooLarge {
			return nil, true, fmt.Errorf("native download exceeds 256 MiB")
		}
		if done.state != browser.DownloadProgressStateCompleted {
			return nil, true, fmt.Errorf("native download was canceled")
		}
		finished = true
		if !regexp.MustCompile(`^[A-Za-z0-9-]+$`).MatchString(done.guid) {
			return nil, true, fmt.Errorf("invalid native download identifier")
		}
		path := filepath.Join(dir, done.guid)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > 256<<20 {
			return nil, true, fmt.Errorf("native download is empty, unavailable or exceeds 256 MiB")
		}
		data, err := os.ReadFile(path)
		return data, true, err
	}
}

func IsPDF(data []byte) bool { return bytes.HasPrefix(data, []byte("%PDF-")) }

func ValidatePDF(data []byte) error {
	if !IsPDF(data) || len(data) < 16 {
		return fmt.Errorf("provider did not return a PDF")
	}
	start := len(data) - 2048
	if start < 0 {
		start = 0
	}
	if !bytes.Contains(data[start:], []byte("%%EOF")) {
		return fmt.Errorf("downloaded PDF appears truncated")
	}
	return nil
}
