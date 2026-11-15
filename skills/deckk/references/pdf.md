# Direct PDF links

Use the host's browser download facility on the user-authorized PDF link, preserving original bytes. If already open in a PDF viewer, use its native download control; do not screenshot a single visible PDF page and call it complete.

If a clickable source link is needed, read `scripts/download_link.js` and evaluate `(<source>)(authorizedURL)` in a normal page in the same browser session. It creates a visible link with a download hint; it does not fetch, click or send content anywhere. Use its returned selector with the normal download tool. Same-origin links with a download attribute and servers returning an attachment work; some cross-origin inline PDFs instead navigate. In that case use the browser PDF viewer's download control, or the host's separately authorized HTTP-to-file facility if one exists. Report missing host support rather than inventing a file path or installing another browser.

Allow normal authentication redirects. Do not export browser cookies, copy signed URLs into a separate shell downloader, or follow unrelated download links. A .pdf suffix or Content-Type alone does not prove success: validate the file in the workspace with [capture.md](capture.md). HTML denial pages, truncation and encrypted PDFs must be reported.
