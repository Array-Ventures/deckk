/* Evaluate as `(<this function source>)(provider)` in the current tab.
 * Read-only apart from data-deckk selector markers. No requests or form submissions.
 */
(async function inspectDeck(providerHint = null) {
  const host = location.hostname.toLowerCase();
  const domain = name => host === name || host.endsWith('.' + name);
  const inferred = domain('papermark.com') || domain('papermark.io') ? 'papermark' :
    domain('pitch.com') ? 'pitch' : domain('canva.com') || domain('canva.link') ? 'canva' :
    host === 'docs.google.com' && location.pathname.startsWith('/presentation/') ? 'google-slides' :
    host === 'drive.google.com' ? 'google-drive' : domain('notion.site') || domain('notion.so') ? 'notion' : null;
  const provider = providerHint || inferred;
  const base = { provider, origin: location.origin, documentPath: location.pathname };
  const visible = el => !!el && el.getClientRects().length > 0 && getComputedStyle(el).visibility !== 'hidden' &&
    getComputedStyle(el).display !== 'none' && Number(getComputedStyle(el).opacity) > 0;
  const excluded = el => !!el.closest('form[id*="feedback" i],form[class*="feedback" i],form[id*="comment" i],form[class*="comment" i],[role="complementary"]');
  const text = el => (el?.innerText || el?.textContent || '').trim().replace(/\s+/g, ' ');
  const all = selector => [...document.querySelectorAll(selector)].filter(el => visible(el) && !excluded(el));
  const first = selector => all(selector)[0];
  const mark = (el, name) => {
    document.querySelectorAll(`[data-deckk-${name}]`).forEach(node => node.removeAttribute(`data-deckk-${name}`));
    if (!el) return null;
    el.setAttribute(`data-deckk-${name}`, '');
    return `[data-deckk-${name}]`;
  };
  const gate = kind => ({ ...base, status: 'gate', gate: { kind, requiresHuman: true } });
  if (!['papermark', 'pitch', 'canva', 'google-slides', 'google-drive', 'notion', 'pdf'].includes(provider)) {
    return { ...base, status: 'unsupported', reason: 'Unknown provider. A branded Papermark or Notion domain needs an explicit provider hint.' };
  }
  if (all('h1,h2,[role="alert"]').some(el => /^(403(?: error)?|access denied|request blocked|you need access|request access|page not found)/i.test(text(el))) ||
    /^ERROR: The request could not be satisfied/i.test(document.title)) return gate('access-denied');
  if (first('iframe[src*="challenges.cloudflare.com"],#challenge-running,input[name="cf-turnstile-response"]') ||
    all('h1,h2').some(el => /verify you are human|checking your browser/i.test(text(el)))) return gate('challenge');
  if (first('#agreement,input[name*="agreement" i],[role="dialog"] input[type="checkbox"],.nda-agreement') ||
    all('button').some(el => /^(open signing|start signing|sign agreement|i agree)$/i.test(text(el)))) return gate('agreement');
  // Only inspect access forms; never return input values or fill them here.
  const target = first('input[autocomplete="one-time-code"],input[type="password"],input[type="email"],input[name="email"],input#name,input[name="code"],input[name="otp"]');
  const form = target?.closest('form') || (provider === 'papermark' ? all('form').find(el => el.querySelector('input:not([type="hidden"]),textarea,select')) : null);
  if (target && !form) return gate('credentials');
  if (form) {
    if ([...form.querySelectorAll('input[type="checkbox"],[role="checkbox"]')].some(visible)) return gate('agreement');
    const fields = [...form.querySelectorAll('input,textarea,select')].filter(el => visible(el) && !['hidden', 'submit', 'button'].includes(el.type)).map((el, index) => ({
      kind: el.autocomplete === 'one-time-code' || /^(code|otp|verification_code)$/.test(el.name) ? 'code' :
        el.type === 'password' ? 'secret' : el.type === 'email' || el.name === 'email' ? 'email' : el.name === 'name' || el.id === 'name' ? 'name' : 'custom',
      key: el.name || el.id || null, type: el.type || el.tagName.toLowerCase(), required: el.required,
      label: text(el.labels?.[0]) || el.getAttribute('aria-label') || el.placeholder || null,
      selector: mark(el, `field-${index}`)
    }));
    const submit = [...form.querySelectorAll('button[type="submit"],input[type="submit"],button:not([type])')].find(visible);
    const rejected = [...form.querySelectorAll('[role="alert"],[class*="error"]')].some(el => visible(el) && /incorrect|invalid|expired|denied|not allowed/i.test(text(el)));
    return { ...base, status: 'gate', gate: { kind: rejected ? 'rejected' : fields.some(el => el.kind === 'code') ? 'verification-code' : 'credentials', fields, submit: mark(submit, 'submit'), canSubmit: !!submit && !submit.disabled } };
  }
  if (all('h1,h2,[role="alert"]').some(el => /check your (email|inbox)|verification (email|link) sent/i.test(text(el)))) return gate('verification-link');
  const named = el => [text(el), el.getAttribute('aria-label'), el.title].filter(Boolean);
  const control = re => all('a,button,[role="button"],[role="menuitem"]').find(el => !el.disabled && el.getAttribute('aria-disabled') !== 'true' && named(el).some(value => re.test(value)));
  const download = all('a,button,[role="menuitem"]').find(el => !el.disabled && el.getAttribute('aria-disabled') !== 'true' &&
    (el.matches('a[download]') || named(el).some(value => /^download(?:\s+(?:pdf|file|document|presentation|deck|original)\b.*)?$/i.test(value))));
  const common = { ...base, download: mark(download, 'download'), more: mark(control(/^(more|more options|options|file|share)$/i), 'more') };
  if (provider === 'google-slides') {
    // Never derive an authenticated Google export from an arbitrary branded host.
    const match = host === 'docs.google.com' && /^\/presentation\/(d\/(?:e\/)?[A-Za-z0-9_-]+)(?:\/|$)/.exec(location.pathname);
    return { ...common, status: 'native-only', exportURL: match ? `${location.origin}/presentation/${match[1]}/export/pdf` : null,
      reason: match ? 'Use the native PDF export in this browser session.' : 'No supported Slides ID. Use the visible File > Download menu.' };
  }
  if (provider === 'google-drive') {
    const id = host === 'drive.google.com' && (/^\/file\/d\/([A-Za-z0-9_-]+)(?:\/|$)/.exec(location.pathname)?.[1] || new URL(location.href).searchParams.get('id'));
    return { ...common, status: 'native-only', exportURL: id && /^[A-Za-z0-9_-]+$/.test(id) ? `https://drive.usercontent.google.com/download?${new URLSearchParams({id, export: 'download'})}` : null,
      reason: 'Download the original PDF; follow normal confirmation/sign-in UI. Non-PDF files need their native export.' };
  }
  if (provider === 'pdf') return { ...common, status: 'native-only', reason: 'Use the browser PDF download control or an authorized source link; validate the downloaded bytes.' };
  if (provider === 'notion') {
    const content = first('.notion-page-content');
    const scroller = first('.notion-scroller.vertical');
    if (!content || !scroller) return { ...common, status: 'unsupported', reason: 'No supported Notion document scroller. Check access or use native export.' };
    const r = scroller.getBoundingClientRect();
    const contentRect = content.getBoundingClientRect();
    // Measure the content bottom, excluding Notion's expandable trailing scroller padding.
    const height = contentRect.bottom - r.top + scroller.scrollTop;
    const offset = scroller.scrollTop;
    const pending = [...content.querySelectorAll('img')].some(img => !img.complete || !img.naturalWidth);
    const collapsed = [...content.querySelectorAll('[role="button"][aria-expanded="false"]')].filter(visible).length;
    if (!fullyVisible(scroller) || height <= 0 || height > 40000 || r.height < 100) return { ...common, status: 'unsupported', reason: 'Document bounds do not fit the supported capture range.' };
    const fingerprint = await digest(content.outerHTML);
    return { ...common, status: pending || document.fonts?.status === 'loading' ? 'loading' : 'scroll-ready',
      selector: mark(scroller, 'scroll'), fingerprint, offset, viewportHeight: r.height, contentHeight: height,
      atEnd: offset + r.height >= height - 1, collapsed, blocks: content.querySelectorAll('[data-block-id]').length };
  }
  function fullyVisible(el) {
    const r = el.getBoundingClientRect();
    return visible(el) && r.width >= 200 && r.height >= 100 && r.left >= 0 && r.top >= 0 && r.right <= innerWidth + 1 && r.bottom <= innerHeight + 1;
  }
  async function digest(value) {
    if (!crypto.subtle) throw new Error('This browser context cannot fingerprint the viewer.');
    const bytes = await crypto.subtle.digest('SHA-256', new TextEncoder().encode(value));
    return [...new Uint8Array(bytes)].map(n => n.toString(16).padStart(2, '0')).join('');
  }
  // Counters, rather than the number of mounted slides, prove the deck total.
  const counters = all('[data-page-counter],[class*="slide-count"],[class*="page-counter"],[aria-live],button,[role="button"]');
  let counter = null;
  if (provider === 'papermark') {
    const spans = all('div').filter(el => el.children.length === 3 && [...el.children].every(child => child.tagName === 'SPAN'));
    counters.unshift(...spans);
  }
  for (const line of [...counters.map(text), ...(document.body.innerText || '').split('\n')]) {
    const match = /^\s*(?:(?:page|slide)\s+)?(\d{1,4})\s*(?:\/|of)\s*(\d{1,4})\s*$/i.exec(line);
    if (match) { counter = { index: Number(match[1]), total: Number(match[2]) }; break; }
  }
  if (provider === 'canva') {
    const slider = all('[role="slider"]').find(el => /page/i.test(el.getAttribute('aria-label') || ''));
    if (slider) counter = { index: Number(slider.getAttribute('aria-valuenow')), total: Number(slider.getAttribute('aria-valuemax')) };
  }
  let slide;
  let content;
  if (provider === 'pitch') slide = all('.player-v2--stage .slide-wrapper,.slide-wrapper,.slide').filter(fullyVisible)[0];
  if (provider === 'canva') {
    const pages = all('[data-page-id]').filter(el => /^\d+$/.test(el.dataset.pageId) && fullyVisible(el));
    slide = pages.sort((a,b) => b.getBoundingClientRect().width*b.getBoundingClientRect().height-a.getBoundingClientRect().width*a.getBoundingClientRect().height)[0];
    if (slide && counter && Number(slide.dataset.pageId) + 1 !== counter.index) return { ...common, ...counter, status: 'loading', reason: 'Canva page and counter disagree.' };
    if (!slide && !document.querySelector('[data-page-id]')) slide = all('[role="img"][aria-label*="page" i],.presentation-page,.slide').filter(fullyVisible)[0];
  }
  if (provider === 'papermark') {
    content = all('img.viewer-image-mobile,img[alt^="Page "],.react-pdf__Page canvas').filter(fullyVisible).sort((a,b) => b.getBoundingClientRect().width*b.getBoundingClientRect().height-a.getBoundingClientRect().width*a.getBoundingClientRect().height)[0];
    // The immediate wrapper includes Papermark's watermark and media overlays.
    slide = content?.parentElement;
    if (content && /blank\.gif(?:\?|$)/i.test(content.currentSrc || content.src || '')) slide = null;
    const number = content && /^Page (\d+)$/i.exec(content.alt || '');
    if (number && counter && Number(number[1]) !== counter.index) return { ...common, ...counter, status: 'loading', reason: 'Papermark page and counter disagree.' };
  }
  if (!counter || !Number.isInteger(counter.index) || !Number.isInteger(counter.total) || counter.index < 1 || counter.total < counter.index || counter.total > 300) {
    return { ...common, status: download ? 'native-only' : 'unsupported', reason: 'No authoritative page counter between 1 and 300 pages.' };
  }
  if (!slide || !fullyVisible(slide)) return { ...common, ...counter, status: 'loading', reason: 'No complete visible slide; fit it in the viewport or use a native PDF.' };
  if ([...slide.querySelectorAll('img')].some(img => !img.complete || !img.naturalWidth) || document.fonts?.status === 'loading' ||
    slide.querySelector('video,audio,[aria-busy="true"]')) return { ...common, ...counter, status: 'loading', reason: 'Images, fonts, or media are not ready for a static capture.' };
  const signature = () => JSON.stringify({ html: slide.outerHTML.replace(/ data-deckk-[\w-]+="[^"]*"/g,''), canvases: [...slide.querySelectorAll('canvas')].map(el => el.toDataURL()) });
  let before, after;
  try {
    before = signature();
    await new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve)));
    after = signature();
  } catch (_) { return { ...common, ...counter, status: 'unsupported', reason: 'The rendered slide cannot be fingerprinted.' }; }
  if (before !== after) return { ...common, ...counter, status: 'loading', reason: 'The slide changed while inspecting.' };
  const r = slide.getBoundingClientRect();
  return { ...common, ...counter, status: 'ready', fingerprint: await digest(after), selector: mark(slide,'slide'), width: r.width, height: r.height,
    next: mark(control(/^(next|next page|next slide|right)$/i), 'next'), previous: mark(control(/^(previous|previous page|previous slide|back)$/i), 'previous') };
})
