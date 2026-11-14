package docsend

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/holman/deckk/internal/adapter"
)

// Scope every submission to its access form. Never submit a feedback/chat
// widget, or use a page-wide "Continue" button as an authentication control.
const accessHelpersJS = `
const visible = el => !!el && el.getClientRects().length > 0 &&
  getComputedStyle(el).visibility !== 'hidden' && getComputedStyle(el).display !== 'none';
const excluded = el => !!el.closest('form[id*="feedback" i], form[class*="feedback" i], form[id*="comment" i], form[class*="comment" i], form[id*="doc-chat" i], form[class*="doc-chat" i]');
const find = selector => Array.from(document.querySelectorAll(selector)).find(el => visible(el) && !excluded(el));
const code = find('input[name="link_auth_form[verification_code]"], input[name="verification_code"], input[autocomplete="one-time-code"]');
const signing = Array.from(document.querySelectorAll('button')).find(el => visible(el) && /^(start signing|I agree\. Complete signing)$/i.test((el.innerText || '').trim())) || find('form.js-signature-modal-form') || find('button.document-field_display');
const nda = find('.nda-agreement');
const password = find('input[name="link_auth_form[passcode]"], input[name="passcode"], input[type="password"]');
const email = find('input[type="email"], input[name="link_auth_form[email]"], input[name="email"], input#email') ||
  Array.from(document.querySelectorAll('input[type="text"], input:not([type])')).find(el => visible(el) && !excluded(el) &&
    /email/i.test([el.placeholder, el.getAttribute('aria-label'), ...Array.from(el.labels || []).map(l => l.textContent)].join(' ')));
const target = signing || code || nda || password || email;
const form = target && (target.closest('form') || target.querySelector('form'));
const kind = signing ? 'signature' : code ? 'verification' : nda ? 'nda' : password || email ? 'credentials' : '';
const sameForm = el => !!el && !!form && el.closest('form') === form;
`

const detectAccessGateJS = `(() => {
` + accessHelpersJS + `
  const errors = Array.from(document.querySelectorAll('#prompt .alert_content, #prompt .error-message, .alert_content, form [role="alert"], form .error-message'));
  const rejected = errors.find(el => visible(el) && !excluded(el) &&
    /invalid|incorrect|expired|not authori[sz]ed|access denied|restricted|not allowed|unable|already.{0,20}used|no longer/i.test(el.textContent || ''));
  if (rejected) return {kind: 'rejected', text: (rejected.innerText || '').slice(0, 500)};
  return {kind, email: sameForm(email), passcode: sameForm(password),
    action: form ? form.action : '',
    text: nda ? (nda.querySelector('.nda-agreement__content') || nda).innerText.trim() : ''};
})()`

const submitAccessGateJS = `(values) => {
` + accessHelpersJS + `
  if (!form || kind !== values.kind) return 'access form changed or is missing';
  const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
  const fill = (el, value) => {
    setter.call(el, value);
    for (const type of ['input', 'change', 'blur']) el.dispatchEvent(new Event(type, {bubbles: true}));
  };
  if (kind === 'verification') fill(code, values.code);
  if (kind === 'credentials') {
    if (sameForm(email)) fill(email, values.email);
    if (sameForm(password)) fill(password, values.passcode);
  }
  if (kind === 'nda' || kind === 'signature') return 'agreement gates require manual handling';
  const button = Array.from(form.querySelectorAll('button[type="submit"], input[type="submit"], button:not([type])')).find(visible);
  if (button && button.disabled) return 'access form submit button is disabled';
  if (!form.checkValidity()) return 'access form requires additional or valid input';
  if (button) button.click();
  else form.requestSubmit();
  return '';
}`

type accessGate struct {
	Kind     string
	Email    bool
	Passcode bool
	Action   string
	Text     string
}

type gateValues struct {
	Kind     string `json:"kind"`
	Email    string `json:"email"`
	Passcode string `json:"passcode"`
	Code     string `json:"code"`
}

func handleAccessGates(ctx context.Context, opts adapter.Options) error {
	deadline := time.Now().Add(8 * time.Second)
	var last accessGate
	attempts := 0
	for {
		var gate accessGate
		if err := chromedp.Run(ctx, chromedp.Evaluate(detectAccessGateJS, &gate)); err != nil {
			return fmt.Errorf("detect DocSend access gate: %w", err)
		}
		if gate.Kind == "rejected" {
			return fmt.Errorf("DocSend rejected access: %s", safeGateText(gate.Text))
		}
		if gate.Kind == "signature" {
			return fmt.Errorf("DocSend requires an electronic signature; complete the agreement manually. No signature was submitted")
		}
		if gate.Kind == "" {
			// Wait for the viewer/Space rather than mistaking a gap between
			// sequential forms for completed authentication.
			var slide slideRect
			var docs []spaceDoc
			if err := chromedp.Run(ctx, chromedp.Evaluate(findVisibleSlideJS, &slide), chromedp.Evaluate(spaceDocsJS, &docs)); err != nil {
				return err
			}
			if (slide.Found && slide.Ready) || len(docs) > 0 {
				return chromedp.Run(ctx, chromedp.Evaluate(hideOverlaysJS, nil))
			}
		} else if gate != last {
			if attempts >= 8 {
				return fmt.Errorf("too many DocSend access steps; stopped without retrying credentials")
			}
			values := gateValues{Kind: gate.Kind}
			switch gate.Kind {
			case "credentials":
				if gate.Email && opts.Email == "" {
					return fmt.Errorf("this deck requires an email; supply --email")
				}
				if gate.Passcode && opts.Passcode == "" {
					return fmt.Errorf("this deck requires a passcode; supply --passcode")
				}
				values.Email, values.Passcode = opts.Email, opts.Passcode
			case "verification":
				values.Code = strings.TrimSpace(opts.VerificationCode)
				if values.Code == "" && opts.RequestVerificationCode != nil {
					var err error
					values.Code, err = opts.RequestVerificationCode(ctx)
					if err != nil {
						return fmt.Errorf("obtain DocSend verification code: %w", err)
					}
				}
				values.Code = strings.TrimSpace(values.Code)
				if values.Code == "" {
					return fmt.Errorf("this deck requires an emailed verification code; supply --verification-code or use the CLI prompt")
				}
			case "nda":
				return fmt.Errorf("this deck requires NDA acceptance; complete it manually:\n%s", gate.Text)
			}
			// JSON encoding preserves arbitrary credentials as data, including
			// quotes, backslashes and line separators, without logging them.
			payload, err := json.Marshal(values)
			if err != nil {
				return err
			}
			var result string
			if err := chromedp.Run(ctx, chromedp.Evaluate("("+submitAccessGateJS+")("+string(payload)+")", &result)); err != nil {
				return fmt.Errorf("submit DocSend access gate: %w", err)
			}
			if result != "" {
				return fmt.Errorf("submit DocSend access gate: %s", result)
			}
			last = gate
			attempts++
			deadline = time.Now().Add(15 * time.Second)
		}
		if time.Now().After(deadline) {
			if attempts == 0 && gate.Kind == "" {
				return nil // Let slide discovery report an unsupported viewer.
			}
			return fmt.Errorf("DocSend access gate did not clear; credentials may have been rejected or another access step is required")
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(200*time.Millisecond)); err != nil {
			return err
		}
	}
}

func safeGateText(text string) string {
	text = regexp.MustCompile(`https?://\S+`).ReplaceAllString(text, "[link]")
	return regexp.MustCompile(`[A-Za-z0-9_-]{24,}`).ReplaceAllString(text, "[redacted]")
}
