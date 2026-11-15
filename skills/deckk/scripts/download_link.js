/* Evaluate as `(<this function source>)(authorizedURL)` then use the host's download tool.
 * Creates a visible link only; does not click, fetch, or change browser download policy.
 */
(function prepareDownload(authorizedURL) {
  const url = new URL(authorizedURL, location.href);
  if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password) throw new Error('A credential-free HTTP(S) download URL is required.');
  document.querySelector('[data-deckk-export]')?.remove();
  const link = document.createElement('a');
  link.href = url.href;
  link.download = 'deck.pdf';
  link.referrerPolicy = 'no-referrer';
  link.textContent = 'Download requested document';
  link.setAttribute('data-deckk-export', '');
  link.style.cssText = 'position:fixed;bottom:8px;left:8px;z-index:2147483647;background:white;color:black;padding:8px';
  document.body.append(link);
  return { selector: '[data-deckk-export]' };
})
