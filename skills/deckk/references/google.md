# Google Slides and Drive

Use the current browser session, including any existing authorized Google login. Inspect with [viewer.md](viewer.md). Respect sign-in, access-denied and download restrictions; importing a skill grants no account access.

For Google Slides, prefer File > Download > PDF or the inspector's `exportURL`, derived only from a real docs.google.com presentation path. This preserves native text and slide sizing. Published `/presentation/d/e/...` URLs have a separate ID shape; if that export does not work, use the visible published-page controls or obtain an exportable sharing link. Do not claim every published link has a PDF export.

For Drive, prefer the original file's Download control. `exportURL` is a native download endpoint derived only from a drive.google.com file ID. Do not auto-skip security/virus warnings. Handle confirmations using the normal browser UI and permissions. If Drive redirects a native Slides file to docs.google.com, use the Slides path. Other file types need their own app's export; do not rename a PPTX or HTML access page to PDF.

If the host downloads from a selector rather than a URL, use `scripts/download_link.js`: evaluate `(<source>)(authorizedURL)` with the inspected export URL to create a visible link, then pass its returned selector to the normal download tool. This helper does not fetch or click. Browser cross-origin download rules and server response headers still apply; a navigated page is not a downloaded file. Prefer the actual app menu if the link does not trigger a download.

Obtain the sandbox path and validate the native bytes using [capture.md](capture.md). Do not fall back to screenshotting only the visible editor or Drive preview. If no complete native export is available, report that limitation.
