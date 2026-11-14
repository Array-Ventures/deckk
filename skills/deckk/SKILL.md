---
name: deckk
description: Download shared presentations and documents as local PDFs with the deckk CLI. Use for DocSend, Papermark, Pitch, Canva, Google Slides, Google Drive file links, public Notion pages, or direct PDF URLs when the user wants a saved PDF.
---

# deckk

Use the local `deckk` CLI to save the requested document in the agent's
workspace. Conversion runs locally; do not substitute a hosted conversion
service. Visit only links the user has authorized for the current task.

## Prepare

Check `deckk --help` for the installed binary's supported flags. If it is
missing, build from this checkout with `go build -o <binary-path> ./cmd/deckk`,
or follow the repository's installation instructions. The DocSend and Papermark passcode,
verification-code and verification-link flags require a build containing those additions.

Choose an absolute output path inside the workspace the agent can access.
Avoid replacing an existing file unless the user requested it. Use the
user's supplied or previously authorized email for access gates. When none
is known, ask before visiting an email-gated link: deckk otherwise defaults
to the machine's `git user.email`.

## Download

Put flags **before** the URL; Go's flag parser stops at the first positional
argument. Quote the URL, output path and supplied values as shell arguments.

```bash
deckk --email '<authorized-email>' -o '<workspace>/deck.pdf' '<document-url>'
```

Omit `--email` for a known public document. For a DocSend or Papermark passcode gate, use
`--passcode '<provided-passcode>'`. For longer downloads or time to retrieve
a verification code, increase `--timeout`, for example `--timeout 10m`.

For branded Papermark links, select `--provider papermark`; their URLs may
use a custom domain and slug instead of `/view/`. Legacy `papermark.io`
links are recognized automatically. Use an individual document link for a
data room; this CLI does not choose or bulk-export a room's documents.

If a Papermark form requests a visitor name, use `--name '<provided-name>'`.
For other text fields, provide `--field '<identifier>=<provided-value>'`
once per field, using the identifier reported by the CLI. It supports text,
long-text, number, URL and phone inputs. Do not invent values. Checkbox and
agreement controls require manual handling, including custom checkboxes.

The CLI currently prints progress to stderr and `PDF saved to <path>` to
stdout, with exit code zero on success. It does not provide JSON results or
persisted sessions. Treat errors or timeouts as failures, even if a partial
file exists.

## Access gates

- **Email/passcode:** Supply values authorized by the user. If a gate asks
  for information that is missing, obtain it before retrying. Rejected
  credentials are a stopping condition until the user provides a correction.
- **DocSend/Papermark verification code:** Run the command through an execution tool
  that supports interactive stdin and retains the process across turns.
  When the CLI prompts, ask the user for the emailed code and send it to
  that same process followed by a newline. Keep the browser and process
  alive while waiting. Read email only when separately authorized. A code
  already available can be supplied with `--verification-code '<code>'`.
- **Agreement or electronic signature:** Stop and report that the agreement
  must be completed manually. The CLI does not accept NDAs or sign documents.
  Do not treat a displayed agreement as the requested deck.
- **DocSend emailed link:** Some links require opening an authentication URL
  instead of entering an OTP. The CLI can prompt for this when the first slide
  is denied. Obtain the newest unopened link from the user (or their email
  only when authorized) and send it to the same running process. A 403 can also
  mean access was denied for another reason; stop if there is no verification
  email. A link already obtained can be passed with `--verification-link`, or
  `--verification-link -` to read it from stdin. Postmark wrappers are decoded
  locally, and verification URLs must point back to the requested document.
- **No interactive execution available:** Explain the limitation when a
  verification prompt occurs. There is no saved session to resume in a new
  command. Do not repeatedly restart the conversion or send new verification
  emails without the user's direction.

Papermark supports email, passcode, and single-input OTP forms. Agreements
require manual handling. Native/raw PDFs, raw PNG/JPEG images, and PDF slide
images are supported. Video, spreadsheet/HTML conversion, bulk data-room
exports and external signing flows are not supported. Private/sign-in-only Google, Canva
and Notion documents are not supported. Report unsupported access steps
instead of treating them as a successful download.

## Return the PDF

After a successful exit, check that the output is a readable, nonempty PDF
and inspect its page count with an available local PDF utility. Compare
against the viewer's reported count when present; report any mismatch as
incomplete. Screenshot-based adapters without an authoritative page count can still
return fewer pages after a loading failure.

When the provider offers a native PDF download, the CLI prefers it and
preserves the original bytes. Papermark documents with a configured watermark
require that native PDF path; if it is unavailable, report the limitation
rather than dropping the watermark by extracting the underlying images.

Return the saved path and page count. If the agent host supports file
attachments or download links, attach or link that same local file using
the host's supported mechanism. A path on a remote machine is not by itself
a download link for the user; transfer the file through the host's file
facility when needed. Read or summarize the deck only when requested.

DocSend and Papermark native/raw PDFs may preserve text and links; image
extraction produces image-based PDFs. Pitch and Canva use screenshots. Google
Slides exports, direct PDFs and Notion's printed PDF may preserve text.
Do not claim searchable text or complete capture without checking.
