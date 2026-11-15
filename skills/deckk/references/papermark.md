# Papermark

Open the authorized document link in the existing browser. For a branded domain, confirm it is a Papermark document and invoke the [viewer inspector](viewer.md) with `"papermark"`; normal papermark.com/.io hosts are detected automatically. A data-room folder is not one document: ask which document to save rather than exporting the room.

Use normal browser tools to fill the reported email, password, name, OTP and custom fields with values supplied or authorized for this task. Inspect labels and required fields; never invent a name, silently submit defaults or interact with a checkbox/agreement. Email verification is supported as an access-form state, but requires the actual code and the same browser session. Do not request repeated emails. Signing and agreements remain manual blockers.

Prefer the native PDF download, opening its menu if necessary. Validate its sandbox file with [capture.md](capture.md). If the viewer has no reliable page total, a native PDF can still be validated without a viewer-count comparison.

For rendered pages, the inspector uses the visible `viewer-image-mobile` or PDF canvas and its enclosing page wrapper, including watermark and media overlays. It checks the image's `Page N` label against the counter and rejects blank placeholders. Fit the entire wrapper in the viewport. Use page controls or normal viewer scrolling, then confirm the exact next index before capturing. Lazy pages are loaded through the normal viewer rather than signed-URL API calls. This path preserves visible watermarks and records normal viewer activity.

Follow the shared [stable capture procedure](capture.md). If a responsive layout, single-image view or non-PDF asset lacks a proved page count, prefer its native download; report unsupported content instead of treating a data-room listing or poster frame as a complete deck.
