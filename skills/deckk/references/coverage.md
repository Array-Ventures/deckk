# Verification and limits

The browser skill ports provider workflows from this repository's CLI adapters. It uses the host browser's rendering and download tools rather than starting another browser. This is a different execution path: earlier CLI live tests do not prove the browser skill works on those same sites.

| Provider | Browser skill path | Evidence |
| --- | --- | --- |
| DocSend | Native PDF or stable paged screenshots | Local access/capture/native fixtures and a real Evebot local-fixture run. The latest live user-owned deck attempt returned CloudFront 403 before the viewer. |
| Papermark | Access fields, native PDF or watermarked page-wrapper screenshots | Local browser fixtures; selectors checked against the available Papermark source snapshot. No new live run. |
| Pitch | Native PDF or DOM/SVG slide screenshots | Local browser fixtures. Earlier live coverage was the CLI path. |
| Canva | Native PDF or verified page-element screenshots | Local browser fixtures. Earlier live coverage was the CLI path; unfamiliar legacy layouts fail explicitly. |
| Google Slides | Native PDF export in the existing session | Local URL/gate/download fixtures. A provider response may still reject export. |
| Google Drive | Native original-file download | Local ID/gate/download fixtures. Supports existing PDFs and Slides redirects, not arbitrary Office conversion. |
| Notion | Native export or bounded scroll-strip capture | Local scroller/crop/completeness fixtures. Current page only; no recursive child-page or embedded-file conversion. |
| Direct PDF | Native browser download, local validation | Local link/download and PDF validation fixtures. Cross-origin inline PDFs can require native PDF-viewer controls or a host HTTP-to-file facility. |

All screenshot paths require complete source bounds and stable pixel captures. Animated media, unsupported layouts, missing page counts, denied access and missing host capabilities are reported as blockers. No NDA signing, CAPTCHA solving, account provisioning or automatic email resending is implemented. The skill respects the host's permissions; allowing browser evaluation alone does not grant download, account or filesystem access.

Local fixtures exercise known structures and failure modes, not current live-service compatibility. Live verification should use disposable documents owned by the tester, one provider at a time. Test native download and screenshot paths separately, including final page/order and sandbox ingestion. Do not test arbitrary private links or claim a conversion succeeded from a clicked button.
