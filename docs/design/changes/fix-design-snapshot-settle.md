---
branch: fix/design-snapshot-settle
states: []
retired: []
ui: true
---

The prototype harness (`frontend/prototype/harness.ts`) now waits for real
rendering updates before `open(n)` resolves. It used to wait for two
`requestAnimationFrame` callbacks, which the scripted renders' fixed clock
(Playwright's `clock.setFixedTime`) turns into a 16 ms timer. On a slow runner
those "frames" passed before any layout or ResizeObserver delivery, so Folder
states were fingerprinted before the thumbnail strip had measured its "+N"
pill. That is the macOS CI mismatch in 203, 206, 217, 220 and 226. Each wait is
now a fresh IntersectionObserver's first notification followed by a posted
message, so the strips' measurements and React's render of them are in before
the capture.

No state's fingerprint changed: `docs/design/state-hashes.json` differs only
in `sourceDigest`. No state is added, changed or retired.
