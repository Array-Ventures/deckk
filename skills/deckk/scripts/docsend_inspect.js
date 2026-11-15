/* Evaluate this expression in the already-open document tab. It never submits a form. */
(async () => {
  const visible = el => !!el && el.getClientRects().length > 0 &&
    getComputedStyle(el).visibility !== 'hidden' && getComputedStyle(el).display !== 'none' && Number(getComputedStyle(el).opacity) > 0;
  const excluded = el => !!el.closest('form[id*="feedback" i], form[class*="feedback" i], form[id*="comment" i], form[class*="comment" i], form[id*="doc-chat" i]');
  const first = selector => [...document.querySelectorAll(selector)].find(el => visible(el) && !excluded(el));
  const text = el => (el?.innerText || el?.textContent || '').trim().replace(/\s+/g, ' ');
  const mark = (el, name) => {
    document.querySelectorAll(`[data-deckk-${name}]`).forEach(node => node.removeAttribute(`data-deckk-${name}`));
    if (!el) return null;
    el.setAttribute(`data-deckk-${name}`, '');
    return `[data-deckk-${name}]`;
  };
  const base = { provider: 'docsend', origin: location.origin, documentPath: location.pathname };

  if (/^ERROR: The request could not be satisfied/i.test(document.title) ||
      (text(first('h1')) === '403 ERROR' && /request blocked/i.test(document.body.innerText))) {
    return { ...base, status: 'gate', gate: { kind: 'access-denied', requiresHuman: true } };
  }

  const signing = first('form.js-signature-modal-form, button.document-field_display') ||
    [...document.querySelectorAll('button')].find(el => visible(el) && /^(start signing|i agree[.] complete signing)$/i.test(text(el)));
  const agreement = first('.nda-agreement');
  if (signing || agreement) return { ...base, status: 'gate', gate: { kind: 'agreement', requiresHuman: true } };
  if (first('iframe[src*="challenges.cloudflare.com"], input[name="cf-turnstile-response"], #challenge-running') ||
      [...document.querySelectorAll('h1,h2,[role="heading"]')].some(el => visible(el) && /verify you are human|checking your browser/i.test(text(el)))) {
    return { ...base, status: 'gate', gate: { kind: 'challenge', requiresHuman: true } };
  }
  const code = first('input[name="link_auth_form[verification_code]"], input[name="verification_code"], input[autocomplete="one-time-code"]');
  const secret = first('input[name="link_auth_form[passcode]"], input[name="passcode"], input[type="password"]');
  const email = first('input[type="email"], input[name="link_auth_form[email]"], input[name="email"]');
  const target = code || secret || email;
  if (target) {
    const form = target.closest('form');
    const same = el => !!el && !!form && el.closest('form') === form;
    const fields = [];
    for (const [kind, el] of [['code', code], ['email', email], ['secret', secret]]) {
      if (same(el)) fields.push({ kind, selector: mark(el, `field-${kind}`) });
    }
    const submit = form && [...form.querySelectorAll('button[type="submit"],input[type="submit"],button:not([type])')].find(visible);
    const rejection = form && [...form.querySelectorAll('[role="alert"],.error-message,.alert_content')].find(el => visible(el) && /invalid|incorrect|expired|denied|not allowed|already.*used/i.test(text(el)));
    return { ...base, status: 'gate', gate: { kind: rejection ? 'rejected' : code ? 'verification-code' : 'credentials', fields, submit: mark(submit, 'submit'), canSubmit: !!submit && !submit.disabled } };
  }
  if ([...document.querySelectorAll('h1,h2,[role="alert"]')].some(el => visible(el) && /check your (?:inbox|email)|verification (?:email|link) sent/i.test(text(el)))) {
    return { ...base, status: 'gate', gate: { kind: 'verification-link', requiresHuman: true } };
  }

  const matchesDownload = value => /^download(?:\s+(?:(?:this\s+)?(?:pdf|document|file|presentation|deck|original|now)\b.*))?$/i.test(value || '');
  const download = [...document.querySelectorAll('a,button,[role="menuitem"]')].find(el => visible(el) && !el.disabled && el.getAttribute('aria-disabled') !== 'true' &&
    ([text(el), el.getAttribute('aria-label'), el.title, el.getAttribute('data-original-title'), el.getAttribute('data-tooltip-content')].some(matchesDownload) ||
      el.matches('a[download],a#downloadButton,button#downloadButton,a.js-download-document,button.js-download-document')));
  const more = [...document.querySelectorAll('button,[role="button"]')].find(el => visible(el) && /^(more|more options|options)$/i.test(el.getAttribute('aria-label') || el.title || text(el)));

  const counterText = [...document.querySelectorAll('[data-page-counter],[class*="page-number"],[class*="page-counter"],[class*="slide-counter"],[aria-live]')]
    .filter(visible).flatMap(el => (el.innerText || el.textContent || '').split('\n'));
  counterText.push(...(document.body.innerText || '').split('\n'));
  let counter = null;
  for (const line of counterText) {
    const match = /^\s*(?:(?:page|slide)\s+)?(\d{1,4})\s*(?:\/|of)\s*(\d{1,4})\s*$/i.exec(line);
    if (match) { counter = { index: Number(match[1]), total: Number(match[2]) }; break; }
  }
  const common = { ...base, download: mark(download, 'download'), more: mark(more, 'more') };
  if (!counter || counter.index < 1 || counter.total < counter.index || counter.total > 300) {
    return { ...common, status: download ? 'native-only' : 'unsupported', reason: 'No authoritative page counter between 1 and 300 pages.' };
  }

  const candidates = [...document.querySelectorAll('img[class*="page-view"], img, canvas')].filter(el => {
    if (!visible(el) || excluded(el)) return false;
    const rect = el.getBoundingClientRect();
    const source = (el.currentSrc || el.src || '').toLowerCase();
    return rect.width >= 200 && rect.height >= 150 && rect.left >= 0 && rect.top >= 0 &&
      rect.right <= innerWidth + 1 && rect.bottom <= innerHeight + 1 &&
      !/banner|logo|avatar|thumbnail|thumb|icon|powered-by|blank[.]gif/.test(source);
  }).sort((a, b) => {
    const preferred = el => el.matches('img[class*="page-view"]') ? 1 : 0;
    return preferred(b) - preferred(a) || b.getBoundingClientRect().width * b.getBoundingClientRect().height - a.getBoundingClientRect().width * a.getBoundingClientRect().height;
  });
  const slide = candidates[0];
  if (!slide) return { ...common, status: 'loading', ...counter, reason: 'No complete visible slide.' };
  const source = () => slide.tagName === 'CANVAS' ? slide.toDataURL('image/png') : slide.currentSrc || slide.src || '';
  const ready = () => (slide.tagName !== 'IMG' || (slide.complete && slide.naturalWidth > 0)) && (!document.fonts || document.fonts.status === 'loaded');
  if (!ready()) return { ...common, status: 'loading', ...counter, reason: 'Slide image or fonts are still loading.' };
  let before, after;
  try {
    before = source();
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    after = source();
  } catch (_) { return { ...common, status: 'unsupported', ...counter, reason: 'The slide source cannot be verified.' }; }
  if (!before || before !== after || !ready()) return { ...common, status: 'loading', ...counter, reason: 'The visible slide changed while inspecting.' };
  const explicit = slide.closest('[data-page-num]')?.getAttribute('data-page-num');
  if (explicit && Number(explicit) !== counter.index) return { ...common, status: 'loading', ...counter, reason: 'Slide and counter disagree.' };
  if (!crypto.subtle) return { ...common, status: 'unsupported', reason: 'This browser context cannot fingerprint the slide.' };
  const digest = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(after));
  const fingerprint = [...new Uint8Array(digest)].map(n => n.toString(16).padStart(2, '0')).join('');
  const rect = slide.getBoundingClientRect();
  const next = [...document.querySelectorAll('button,a,[role="button"]')].find(el => visible(el) && !el.disabled && el.getAttribute('aria-disabled') !== 'true' && /^(next|next page|next slide|right)$/i.test(el.getAttribute('aria-label') || el.title || text(el)));
  const previous = [...document.querySelectorAll('button,a,[role="button"]')].find(el => visible(el) && !el.disabled && /^(previous|previous page|previous slide|back)$/i.test(el.getAttribute('aria-label') || el.title || text(el)));
  return { ...common, status: 'ready', ...counter, fingerprint, selector: mark(slide, 'slide'), width: rect.width, height: rect.height, next: mark(next, 'next'), previous: mark(previous, 'previous') };
})()
