# Inspect the current viewer

Read `scripts/viewer_inspect.js` relative to the skill root as raw UTF-8 source. It is a function expression, unlike the DocSend IIFE. Invoke it in the current document tab as `(<source>)()` or `(<source>)("papermark")` for a confirmed branded provider. The allowed hints are `papermark`, `pitch`, `canva`, `google-slides`, `google-drive`, `notion`, and `pdf`. A hint selects inspection logic; it does not establish access or permission.

For example, after discovering the actual tools and resolving the installed directory:

```js
const file = await tools.bash({command: 'cat "$HOME/.agents/skills/cap-deckk/scripts/viewer_inspect.js"'});
if (file.exitCode !== 0 || file.truncated) throw new Error("Cannot read the full inspector");
const response = await tools.browser__eval({script: `(${file.stdout})()`});
return response.result;
```

Read the actual host response shape. Do not put shell commands inside JavaScript or paste numbered file displays as source. Keep document bytes in the browser and workspace, returning compact metadata from orchestration. If the orchestration parser rejects a program, correct its JavaScript and retry; do not describe a syntax error as a permission rejection.

The inspector does not fetch, submit, fill or click. It adds `data-deckk-*` attributes to return stable selectors. Normal fill, click, press, scroll and download tools perform authorized actions. A `gate` result reports the access form or manual step. Fill only matching supplied values; stop after a rejection. No gate action is implied by evaluating the inspector.

`ready` includes the current page, authoritative total, complete slide selector and render fingerprint. `loading` means retry after actual readiness, within the capture bounds. `unsupported` means the script cannot prove this layout is complete; use a supported native download or report that limitation. `native-only` permits native validation without claiming a viewer-page comparison. `scroll-ready` belongs to Notion's separate procedure.

Inspect after every navigation. Do not count mounted thumbnails as pages. Do not capture a clipped slide or remove a watermark. Use a normal snapshot to locate controls if names differ or a submenu must be opened; inspect again after opening it. A native control marked `download` must be invoked with the host's download tool rather than a plain click. Keep all files in the agent workspace and validate the result with [capture.md](capture.md).
