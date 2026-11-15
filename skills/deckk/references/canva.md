# Canva

Open the supplied URL and allow normal canva.link redirects. `/edit` links may show an anonymous viewer; do not assume they require login. Use the [viewer inspector](viewer.md) to find a full-size numeric `data-page-id` element and a page counter or page slider. Modern page IDs are zero-based, so the inspector checks `pageId + 1` against the counter. It ignores offscreen mounted pages and refuses a page/counter mismatch.

Prefer Canva's native PDF download if available. Opening Share/Download menus is navigation; use the normal download tool on the final download action. Select the requested complete page range and PDF type. If export is restricted, respect that result.

For capture, use a presentation/view mode or zoom that fits the entire page, then follow [capture.md](capture.md). Use visible navigation or PageDown/ArrowRight only when that viewer responds to them; verify the next index after each action. Do not edit the design, change slide content or use the editor's page count as proof that the canvas is displaying the corresponding page. Read-only access is enough for screenshot capture when the viewer permits it.

Legacy viewers are supported only when a clearly identified `.presentation-page`, `.slide` or labelled page element plus an authoritative counter are present. An arbitrary large rectangle in the application shell is not a safe fallback. Report unsupported layouts explicitly.
