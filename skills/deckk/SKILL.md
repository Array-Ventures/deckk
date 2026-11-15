---
name: deckk
description: Save shared decks as local PDFs. Use browser-native DocSend capture with the host's existing browser tools, or the installed deckk CLI for Papermark, Pitch, Canva, Google Slides, Drive, Notion and direct PDF links.
license: MIT
---

# deckk

Save the requested document as a verified PDF in the agent's workspace, ready for reading, ingestion or further processing. Visit links authorized for the task. Conversion stays in the agent's browser and workspace; do not substitute a hosted conversion service.

For **DocSend**, read [references/docsend.md](references/docsend.md). The bundled browser inspection script works in an already-open tab using the host's browser tools. The PDF helper runs in the workspace with Python 3.11 or newer. This route does not need the Go CLI or a second browser installation.

For **other providers**, read [references/cli-providers.md](references/cli-providers.md). Those adapters currently require the separately installed `deckk` CLI and its prerequisites. Importing this skill does not install the CLI or port those providers to browser scripts.

Use only access values supplied or authorized by the user. Stop on rejected credentials, missing verification information, or an agreement/signature; never accept or sign an NDA. Do not repeatedly trigger verification emails. A gate or partial capture is not a successful conversion.

Prefer a native PDF download when available, preserving the original bytes. Otherwise verify the viewer's complete page count and order before assembling captures. Return the accessible sandbox path, verified page count and whether it is an image-based PDF. Attach or share the file only when the user requests delivery and the host supports it; do not invent a download URL.
