# Notion

Open the authorized page in the existing browser. Prefer Notion's native PDF export if the page and account permit it. Choose whether subpages are included according to the user's request; this skill's capture fallback covers the current page only. Obtain the native file through the host download tool and validate it using [capture.md](capture.md).

For a public page without native export, use the [viewer inspector](viewer.md), passing `"notion"` for a confirmed custom domain. A supported page has `.notion-page-content` inside `.notion-scroller.vertical`. Other layouts need a native export or an explicit unsupported result.

## Screenshot the complete page

The fallback creates an image-based PDF with one page per viewport strip, cropping repeated overlap. It does not preserve selectable text, hyperlinks, embedded files, video, linked databases beyond the rendered view, or child pages. State that scope in the result. If those are needed, request a suitable native export instead.

1. Expand content toggles using normal browser controls, including nested toggles, until the inspector's `collapsed` count is zero. Do not click action buttons, sign agreements or open linked pages while expanding text. If the page cannot be fully expanded, report that limitation before producing a complete-page result.
2. Do a bounded loading pass from top to bottom and back to the top. Use `scripts/notion_scroll.js` as `(<source>)(offset)` or normal scrolling within the reported scroller. Scroll by at most the current `viewportHeight`, inspect after each move, and stop at `atEnd`. This loads lazy blocks and images. Bound the page at 40,000 CSS pixels and 300 strips. If the content height keeps growing or never reaches an end, stop rather than asserting completeness.
3. Start capture at offset zero. Require `scroll-ready`, no collapsed content and a fixed `contentHeight` for the capture pass. If a loading image, layout shift or new lazy content changes the height, restart after another loading pass; allow at most two restarts.
4. Screenshot the returned scroller selector twice as unannotated PNG files accessible in the workspace. Use `pdf_files.py image-info` to require consecutive equal pixel hashes, with at most six captures at each position. Keep the last stable file. Inspect before and after; require the same document, offset, viewport height, content height and fingerprint. Wait for actual readiness rather than trusting a sleep.
5. Save the verified segment metadata immediately. Scroll toward `previousOffset + viewportHeight`, inspect the actual resulting offset, and continue until `atEnd`. Keep overlapping final strips: the local helper crops the already-covered pixels. Stop on a gap, no progress, changing page or content outside this supported layout.

Use the same Python environment described in [capture.md](capture.md). Write an ordered manifest using actual complete inspector results:

```json
{
  "segments": [
    {
      "image": "/workspace/notion-1.png",
      "before": {"status":"scroll-ready","origin":"https://example.notion.site","documentPath":"/example","offset":0,"viewportHeight":600,"contentHeight":900,"fingerprint":"actual-inspector-hash","collapsed":0,"atEnd":false},
      "after": {"status":"scroll-ready","origin":"https://example.notion.site","documentPath":"/example","offset":0,"viewportHeight":600,"contentHeight":900,"fingerprint":"actual-inspector-hash","collapsed":0,"atEnd":false},
      "stablePixelHash": "actual-image-info-hash"
    }
  ]
}
```

This example needs the remaining segment reaching the 900-pixel document end. Do not invent metadata. Assemble only after every strip is captured:

```sh
"$DECKK_ENV/bin/python" "$SKILL_DIR/scripts/pdf_files.py" assemble-scroll /workspace/notion-manifest.json /workspace/notion.pdf
```

The helper verifies identity, fixed height, stable captures, ordered full coverage, overlap cropping and a reached document end before publishing. The output PDF page count is the number of captured strips, not a native Notion page count. Return its sandbox path and scope. If the layout virtualizes content in a way these checks cannot establish, obtain a native export rather than declaring the screenshot PDF complete.
