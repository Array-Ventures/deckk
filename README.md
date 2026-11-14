# deckk

Point it at a URL, get a PDF.

Primarily aimed at pitch decks, so angel investors, venture capitalists, and
other weird people can get the actual deck locally and can upload it to a
portfolio management tool, like [Signed](https://signed.com).

```
$ deckk "https://docsend.com/view/abc123"
PDF saved to ~/Desktop/deck.pdf
```

## Install

**Homebrew (macOS / Linux):**

```
brew install holman/tap/deckk
```

**With Go:**

```
go install github.com/holman/deckk/cmd/deckk@latest
```

Either way, you'll need Google Chrome (or Chromium) installed locally —
`deckk` drives it headlessly in the background. On macOS:

```
brew install --cask google-chrome
```

## Usage

```
deckk [flags] <url>
```

Flags:

- `-o, --output` — output path (default: `~/Desktop/deck.pdf`)
- `--email` — email to use when a deck is gated. Defaults to your
  `git config user.email`. Only sent if the deck actually prompts for one.
- `--provider` — choose an adapter explicitly, for example `--provider papermark`
  for a branded domain whose URL cannot be recognized automatically.
- `--name` — visitor name requested by a Papermark access form.
- `--field identifier=value` — a Papermark text, long-text, number, URL or phone
  access field. Repeat for multiple fields. Values are only filled in matching
  form controls; unknown fields fail. Checkbox/consent controls remain manual.
- `--passcode` — password for a protected DocSend or Papermark deck. Only submitted to
  its access form when requested.
- `--verification-code` — DocSend or Papermark email verification code, if already known.
  Otherwise, the CLI prompts for the code when the verification form appears,
  keeping the browser session open while you retrieve it. It does not read
  your inbox. The total timeout also covers time spent waiting for a code.
- `--verification-link` — an unopened DocSend email authentication link. Direct
  links and Postmark tracking links are supported; tracking links are decoded
  locally and only DocSend is visited. Use `--verification-link -` to read it
  from stdin. If DocSend denies the slide images, the CLI also offers a link
  prompt in the existing browser session. A 403 can have other causes, so stop
  if no verification email was requested. Supplied links must name this deck.
- `--headful` — show the browser window (useful for debugging an adapter)
- `--timeout` — total timeout (default: `5m`)

For example, with an email and passcode (any verification code is prompted
for later):

```
deckk --email you@example.com --passcode 'your-passcode' --timeout 10m https://docsend.com/view/abc123
```

## Agent skill

The bundled [deckk skill](skills/deckk/SKILL.md) teaches an agent to invoke the
CLI, handle access gates, check the saved PDF, and return the file through its
host's attachment or download facility.

Copy `skills/deckk` into your agent's skills directory (for Codex,
`~/.codex/skills/deckk`) and put the CLI on its `PATH`. Build the CLI from this
checkout to use the DocSend and Papermark credential and verification additions.
Homebrew and `go install` install only the binary; copy the skill separately.

Verification codes need an execution tool that can keep an interactive
process open while the user retrieves the code. The CLI does not yet persist
sessions or return structured JSON.

## Adapters

`deckk` picks an adapter based on the URL. Today:

- **docsend** — handles `docsend.com/view/...` decks. Authenticates in local
  headless Chrome and prefers the provider's native Download control when it
  yields a PDF, preserving the original PDF bytes. Otherwise it downloads
  original slide images through the viewer's page-data endpoints. If those
  endpoints are unavailable, it falls back to
  screenshots. A failed page after original-image extraction begins stops
  the download; known page counts must be satisfied. Handles
  email and passcode access forms, verification codes and emailed verification links,
  with access gates checked both on the Space landing page and after opening
  its document. Agreement gates stop for manual completion.
  Rejected credentials stop the download; deckk does not bypass restrictions.
- **canva** — handles `canva.com/design/.../view` decks and `canva.link`
  short links. Loads the public view page, reads the page count from the
  player, then walks through each slide with the arrow key. Canva plays
  per-slide animations on entry, so `deckk` waits for the rendered slide to
  stop changing before screenshotting it (about 5–7s per animated slide).
  Reads the viewer's page counter and checks every page advance, including
  the newer anonymous viewer with a thumbnail strip. If the counter is
  unavailable or navigation stalls, it fails instead of saving a partial deck.
  Both Canva and Pitch wait for visible HTML/SVG images, backgrounds, fonts,
  and stable frames, then recheck the current page and total. A resource that
  fails to load or an animation that never settles stops the export. These
  bounded checks cannot guarantee arbitrary later script or canvas updates.
  Only works for publicly shared view links (no Canva login).
- **google-drive** — handles `drive.google.com/file/d/...` links (and the
  older `open?id=` / `uc?id=` forms). Drive doesn't convert anything, it
  just serves whatever was uploaded, so `deckk` downloads the file directly
  and keeps it if it's a PDF. If the link turns out to point at a native
  Slides deck, it falls through to the Slides export. Anything else (a
  `.pptx`, say) errors out with a hint to export it to PDF first. Works for
  files shared as "anyone with the link".
- **google-slides** — handles `docs.google.com/presentation/d/...` decks.
  No browser involved: Slides exposes the same PDF that File → Download
  produces at an export URL, so `deckk` just downloads it directly. Works
  for decks shared as "anyone with the link" (a sign-in-only deck errors
  out with a hint).
- **notion** — handles public Notion pages: `notion.so/...`,
  `<workspace>.notion.site/...`, and `app.notion.com/p/...` links. These are
  documents rather than slide decks, so instead of screenshotting, `deckk`
  renders the page in headless Chrome and prints it to a real, text-selectable
  PDF. Notion only mounts the blocks currently on screen and collapses
  toggles by default, so `deckk` first grows the browser viewport to the full
  height of the page, clicks open every toggle block and toggle heading
  (including nested ones), waits for images, hides the sign-up banner and top
  bar, and forces the light theme. Only works for pages published to the web
  (Share → Publish); private pages error out with a hint. Wide inline
  databases and tables may be clipped at the page edge.
- **papermark** — recognizes `papermark.com/view/...` and legacy
  `papermark.io/view/...` links, including embed links and individual data-room
  document links. Use `--provider papermark` for branded/custom domains.
  After authentication, it prefers the provider's native PDF download, then
  a raw PDF or image, or original slide images. Page images are obtained from
  the viewer's existing view; additional URLs are fetched in batches of 15.
  Handles email, password, OTP, visitor-name and supplied custom text fields.
  Agreement and checkbox gates stop for manual handling. Missing, duplicate
  or corrupt pages fail; expired image URLs are refreshed once through the
  existing view. When a watermark is configured, a native PDF is required so
  extraction does not silently discard the overlay. Data-room index/bulk
  exports, signing flows, video, spreadsheet and HTML-to-PDF conversion are
  not supported; provide an individual PDF document link instead.
- **pitch** — handles `pitch.com/v/...` decks. Loads the public player,
  reads the slide counter, then walks through each slide with the arrow key
  and screenshots just the slide into a PDF. Only works for publicly shared
  player links. Requires a known slide count, confirms each page before
  capture, and retains intentional duplicate slides. Failed navigation stops
  the export without returning a partial deck.

- **pdf** — the fallback for any `http(s)` URL no other adapter claims. If
  the URL serves a PDF, `deckk` saves it as-is. It matches on scheme rather
  than a `.pdf` suffix because plenty of direct links (CDNs, signed URLs,
  attachment endpoints) don't end in `.pdf`; the bytes are checked instead,
  and a non-PDF response errors out with "no deckk adapter for <host>".

Adding a new adapter means implementing one interface in `internal/adapter` and
registering it. PRs welcome.

Adapters that can return either a native PDF or slides implement
`adapter.DocumentFetcher`, which chooses within the same browser session.
The original image-only `Fetch` method is retained for image callers; native
PDF results require `FetchDocument`. Existing PDF-only adapters continue to
use `adapter.PDFFetcher`.

Papermark integration references: its [viewer response](https://github.com/papermark/papermark/blob/ed19717/app/api/views/route.ts),
[page URL endpoint](https://github.com/papermark/papermark/blob/ed19717/app/api/views/pages/route.ts),
and [access forms](https://github.com/papermark/papermark/tree/ed19717/components/view/access-form).

## License

MIT
