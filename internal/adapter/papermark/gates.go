package papermark

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/holman/deckk/internal/adapter"
)

// Papermark uses a single input underneath its six OTP slots, and a Radix
// checkbox for text agreements. Do not interact with external signing flows.
const gateHelpersJS = `
const visible = el => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden';
const find = selector => [...document.querySelectorAll(selector)].find(el => visible(el) && !el.closest('form[id*="feedback" i], form[id*="comment" i], form[class*="comment" i], form[class*="feedback" i]'));
const code = find('input[autocomplete="one-time-code"], input[name="code"]');
const email = find('input#email, input[name="email"]');
const password = find('input#password, input[name="password"]');
const name = find('input#name, input[name="name"]');
const agreement = find('#agreement[role="checkbox"], input#agreement[type="checkbox"]');
const signing = [...document.querySelectorAll('button')].find(el=>visible(el)&&/^open signing$/i.test((el.innerText||'').trim()));
const target = code || email || password || name || agreement || signing;
const form = (target && target.closest('form')) || [...document.querySelectorAll('form')].find(el=>visible(el)&&!el.matches('[id*="feedback" i],[id*="comment" i],[class*="feedback" i],[class*="comment" i]')&&el.querySelector('input,textarea,select'));
const has = el => !!el && !!form && el.closest('form') === form;
const extras = form ? [...form.querySelectorAll('input,textarea,select')].filter(el=>visible(el)&&![code,email,password,name].includes(el)&&!['hidden','submit','button','checkbox'].includes(el.type)) : [];
const checkbox = form && [...form.querySelectorAll('input[type="checkbox"],[role="checkbox"]')].some(visible);
`

const detectGateJS = `(() => {
` + gateHelpersJS + `
return {email: has(email), password: has(password), name:has(name), code: has(code), agreement: has(agreement)||has(signing), checkbox:!!checkbox,
  extra:form?JSON.stringify(extras.map(el=>({key:el.name||el.id,required:el.required,type:el.type||el.tagName.toLowerCase()}))):'',
  text: has(agreement) ? ((agreement.closest('div').innerText || '').trim() +
    [...agreement.closest('div').querySelectorAll('a[href]')].map(a => '\n'+a.href).join('')) : ''};
})()`

const fillGateJS = `(values) => {
` + gateHelpersJS + `
if (!form) return 'no supported access form';
const fill = (el, value) => {
  const prototype=el.tagName==='TEXTAREA'?HTMLTextAreaElement.prototype:el.tagName==='SELECT'?HTMLSelectElement.prototype:HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(prototype,'value').set.call(el, value);
  el.dispatchEvent(new Event('input', {bubbles:true}));
  el.dispatchEvent(new Event('change', {bubbles:true}));
};
if (has(code)) fill(code, values.code);
else {
  if (has(agreement)||has(signing)||checkbox) return 'agreement or checkbox controls require manual handling';
  if (has(email)) fill(email, values.email);
  if (has(password)) fill(password, values.passcode);
  if (has(name)) fill(name,values.name);
  for (const el of extras) {
    const key=el.name||el.id;
    if (Object.hasOwn(values.fields||{},key)) fill(el,values.fields[key]);
  }
}
return '';
}`

const submitGateJS = `(() => {
` + gateHelpersJS + `
if (!form) return 'missing';
if (has(agreement)||has(signing)||checkbox) return 'manual';
const button = [...form.querySelectorAll('button[type="submit"], input[type="submit"], button:not([type])')].find(visible);
if (button && button.disabled) return 'waiting';
if (!form.checkValidity()) return 'invalid';
if (button) button.click(); else form.requestSubmit();
return 'submitted';
})()`

type accessGate struct {
	Email, Password, Name, Code, Agreement, Checkbox bool
	Text                                             string
	Extra                                            string
}

func waitForDeck(ctx context.Context, calls <-chan apiCall, opts adapter.Options) (apiCall, error) {
	deadline := time.Now().Add(20 * time.Second)
	var last accessGate
	attempts := 0
	for {
		select {
		case call := <-calls:
			if call.Err != nil {
				return apiCall{}, call.Err
			}
			if call.Status >= 400 {
				return apiCall{}, accessError(call.Status)
			}
			if call.Endpoint == "/api/views-dataroom" && call.Req.ViewType != "DOCUMENT_VIEW" {
				return apiCall{}, fmt.Errorf("Papermark data room entry points are not individual documents; provide a document link from inside the room")
			}
			if call.Resp.Type != "email-verification" {
				if len(call.Resp.Pages) > 0 || call.Resp.File != "" || call.Resp.FileType != "" {
					return call, nil
				}
				return apiCall{}, fmt.Errorf("Papermark returned no document; access may be denied or require an unsupported step")
			}
		default:
		}
		var gate accessGate
		if err := chromedp.Run(ctx, chromedp.Evaluate(detectGateJS, &gate)); err != nil {
			return apiCall{}, err
		}
		if gate != (accessGate{}) && gate != last {
			if attempts >= 8 {
				return apiCall{}, fmt.Errorf("too many Papermark access steps")
			}
			values := struct {
				Email    string            `json:"email"`
				Passcode string            `json:"passcode"`
				Code     string            `json:"code"`
				Name     string            `json:"name"`
				Fields   map[string]string `json:"fields"`
			}{Email: opts.Email, Passcode: opts.Passcode, Code: strings.TrimSpace(opts.VerificationCode), Name: opts.Name, Fields: opts.Fields}
			if gate.Agreement {
				return apiCall{}, fmt.Errorf("Papermark requires agreement acceptance:\n%s\nComplete the agreement manually; no acceptance was submitted", gate.Text)
			}
			if gate.Email && opts.Email == "" {
				return apiCall{}, fmt.Errorf("Papermark requires an email; supply --email")
			}
			if gate.Checkbox {
				return apiCall{}, fmt.Errorf("Papermark access form contains checkbox controls; complete them manually")
			}
			if gate.Name && strings.TrimSpace(opts.Name) == "" {
				return apiCall{}, fmt.Errorf("Papermark requires a visitor name; supply --name")
			}
			if !gate.Code {
				var extras []struct {
					Key      string
					Required bool
					Type     string
				}
				if gate.Extra != "" {
					if err := json.Unmarshal([]byte(gate.Extra), &extras); err != nil {
						return apiCall{}, fmt.Errorf("invalid access form metadata")
					}
				}
				available := map[string]bool{}
				for _, field := range extras {
					available[field.Key] = true
					if field.Required && strings.TrimSpace(opts.Fields[field.Key]) == "" {
						return apiCall{}, fmt.Errorf("Papermark requires access field %q; supply --field identifier=value", field.Key)
					}
					if field.Type == "file" {
						return apiCall{}, fmt.Errorf("file-upload access fields require manual handling")
					}
				}
				for key := range opts.Fields {
					if !available[key] {
						return apiCall{}, fmt.Errorf("Papermark access field %q was not found in this form", key)
					}
				}
			}
			if gate.Password && opts.Passcode == "" {
				return apiCall{}, fmt.Errorf("Papermark requires a passcode; supply --passcode")
			}
			if gate.Code && values.Code == "" {
				if opts.RequestVerificationCode == nil {
					return apiCall{}, fmt.Errorf("Papermark requires an emailed code; supply --verification-code")
				}
				var err error
				values.Code, err = opts.RequestVerificationCode(ctx)
				if err != nil {
					return apiCall{}, err
				}
				values.Code = strings.TrimSpace(values.Code)
				if values.Code == "" {
					return apiCall{}, fmt.Errorf("verification code is empty")
				}
			}
			payload, _ := json.Marshal(values)
			var result string
			if err := chromedp.Run(ctx, chromedp.Evaluate("("+fillGateJS+")("+string(payload)+")", &result)); err != nil {
				return apiCall{}, err
			}
			if result != "" {
				return apiCall{}, fmt.Errorf("fill Papermark gate: %s", result)
			}
			// React-controlled inputs can enable the submit button on the next
			// render. Wait for that render before clicking, without resubmitting.
			submitDeadline := time.Now().Add(3 * time.Second)
			for {
				if err := chromedp.Run(ctx, chromedp.Evaluate(submitGateJS, &result)); err != nil {
					return apiCall{}, err
				}
				if result == "submitted" {
					break
				}
				if result != "waiting" || time.Now().After(submitDeadline) {
					return apiCall{}, fmt.Errorf("Papermark access form needs additional input or manual signing")
				}
				if err := chromedp.Run(ctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
					return apiCall{}, err
				}
			}
			last = gate
			attempts++
			deadline = time.Now().Add(20 * time.Second)
		}
		if time.Now().After(deadline) {
			return apiCall{}, fmt.Errorf("Papermark access did not complete; credentials may have been rejected or another step is required")
		}
		if err := chromedp.Run(ctx, chromedp.Sleep(100*time.Millisecond)); err != nil {
			return apiCall{}, err
		}
	}
}

func accessError(status int) error {
	switch status {
	case 401, 403:
		return fmt.Errorf("Papermark rejected access (HTTP %d); check the supplied credentials or session", status)
	case 404, 410:
		return fmt.Errorf("Papermark link was not found, expired or revoked (HTTP %d)", status)
	case 429:
		return fmt.Errorf("Papermark rate limit reached; wait before retrying")
	default:
		return fmt.Errorf("Papermark access request failed (HTTP %d)", status)
	}
}
