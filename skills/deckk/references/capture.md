# Capture and validate local PDFs

Use this workflow for any paged viewer returning `ready`, `index`, `total`, `selector` and `fingerprint`. Native downloads take priority.

## Local PDF helper

Resolve paths relative to this installed skill folder, not the shell's current directory. Set `SKILL_DIR` to that folder and `DECKK_ENV` to a writable virtual environment in the workspace. Reuse a suitable existing environment when available:

```sh
python3 -m venv "$DECKK_ENV"
"$DECKK_ENV/bin/python" -m pip install -r "$SKILL_DIR/scripts/requirements.txt"
"$DECKK_ENV/bin/python" "$SKILL_DIR/scripts/pdf_files.py" inspect /workspace/deck.pdf --expected-pages 3
```

Omit `--expected-pages` when the viewer supplies no authoritative count. Dependency installation may require the host's normal network approval. No system packages or separate browser are required. Another PDF skill is optional; this helper handles assembly and validation itself.

## Capture when no native PDF is available

Start at page 1. Use the inspector's authoritative `index` and `total`; never infer total from mounted images. It supports up to 300 pages and reports unsupported/loading states rather than guessing. If the slide does not fit, use normal viewer zoom/fullscreen controls and inspect again. Do not capture a cropped slide.

For each page:

1. Obtain a `ready` inspector result at the expected index. Keep it as `before`.
2. Screenshot its `selector` as PNG, without annotations. Obtain an accessible local file path. On Evebot, `saveToSandbox: true` returns `sandboxPath` directly; ordinary image results may also be staged as sandbox attachments. The browser-storage path itself may be inaccessible from the sandbox. Other hosts may already return local paths. If the host cannot capture an element or deliver the image file, report the missing capability.
3. Capture again and run `pdf_files.py image-info <first> <second>`. Require consecutive equal `pixelHash` values, using at most six captures per page. Wait for actual readiness when unstable; do not assume a fixed sleep proves completion. Keep the final stable image and its hash.
4. Inspect again as `after`. Require ready status, unchanged document identity, index, total and fingerprint. If they changed, discard that capture and retry within the same bound.
5. Record the manifest entry. Advance using normal next control or the viewer's keyboard navigation, and verify the next inspector index increased by exactly one with the same total. Stop if pages are missing or navigation fails.

Code-mode loops are useful when available: retain file paths and metadata without emitting every slide into model context. Normal browser approvals still apply. Do not fetch private slide URLs from a different environment or bypass the host's access controls.

Write a JSON manifest with precisely one ordered entry for each page:

```json
{
  "expectedPages": 3,
  "pages": [
    {
      "index": 1,
      "image": "/workspace/slide-1.png",
      "before": {"status":"ready","index":1,"total":3,"origin":"https://docsend.com","documentPath":"/view/example","fingerprint":"inspector-fingerprint"},
      "after": {"status":"ready","index":1,"total":3,"origin":"https://docsend.com","documentPath":"/view/example","fingerprint":"inspector-fingerprint"},
      "stablePixelHash": "hash-from-image-info"
    }
  ]
}
```

The abbreviated example needs pages 2 and 3 before assembly will succeed. Copy actual inspector results and hashes; do not invent them. Identical slides at different verified indices are valid.

```sh
"$DECKK_ENV/bin/python" "$SKILL_DIR/scripts/pdf_files.py" assemble /workspace/manifest.json /workspace/deck.pdf
```

Assembly verifies completeness, identity, capture stability, current file hashes and PDF page count before publishing. Existing output files are never overwritten. Report the PDF's accessible workspace path and verified page count so the agent can ingest or process it. Attach it only when the user requests delivery and the host supports it. Captured slides produce an image-based PDF; native downloads may retain searchable text. If any page fails, report the blocker and do not deliver a partial deck as complete.

## Evebot code-mode result shapes

For DocSend, the example below evaluates its complete IIFE directly. For the other providers, invoke the function as described in [viewer.md](viewer.md).

Read the inspector as raw source with a sandbox file-read command when passing it programmatically to evaluation; a numbered `read_file` display is not raw JavaScript. For example, after discovering the tools and resolving the installed skill path:

```js
const file = await tools.bash({ command: 'cat "$HOME/.agents/skills/cap-deckk/scripts/docsend_inspect.js"' });
if (file.exitCode !== 0 || file.truncated) throw new Error("Cannot read complete inspector source");
const inspection = await tools.browser__eval({ script: file.stdout });
return inspection.result;
```

Use the actual installed path if it differs. Evaluation returns the inspector state inside `result`. An image tool called in code mode returns `{ output, images }`; use `output.sandboxPath` for the file and do not emit `images` during bulk capture. Checkpoint each verified page's metadata to the manifest immediately, so later calls do not need to reconstruct earlier state from chat. Keep batches within the host's tool-call limit.
