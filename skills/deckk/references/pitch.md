# Pitch

Open the supplied public presentation link, normally `/v/...`, and use the [viewer inspector](viewer.md). It selects `.slide-wrapper` or `.slide` in the player and reads the visible slide counter. A missing counter is unsupported; mounted SVGs and thumbnails do not establish the deck total.

Prefer an available native PDF download. Otherwise start at slide 1, fit the whole slide in view and follow [capture.md](capture.md). Advance with the reported next control or ArrowRight in the focused player, verifying the exact next index. DOM/SVG text, images, fonts and background layers are included in the element screenshot. Wait for transitions and visual stability. Pause media normally; the inspector stops on active media elements rather than declaring a moving slide complete.

Dismiss ordinary overlays with normal controls where permitted. Consent, access and login dialogs need handling according to their meaning; never hide them with CSS or accept tracking terms merely to capture. Do not crop a dialog into a slide or claim a private/paid flow was tested from a public-link test.
