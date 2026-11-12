---
branch: feat/choix-2-desktop-design-463af0
states: [11-17, 30-36, 100-122, 200-229, 300-308, 310-318, 400-409, 500-520, 600-605, 700-712, 720, 800-807]
retired: []
ui: true
---

Choix 2.0's design system and clickable prototype: the tokens, fonts and
shared components under `frontend/src/design/`, every screen state of the
desktop app as a numbered prototype scenario (welcome, Library, Folder, Focus,
Compare, Settings, overlays, native replicas and the component gallery), the
automated state checks and flows, the per-state fingerprints in
`docs/design/state-hashes.json`, and the approval gate (`tools/design/gate.py`)
that holds the app's UI to the approved states.

Every registered state is new on this branch, so every state is listed and
needs the owner's approval. No state is retired.
