# choix app icon

Three photo prints of the same sunset. The two at the back are faded, and the
one in front is in full colour with a green pick badge. The green is the UI's
pick colour, `oklch(0.72 0.18 145)`, and the background is the UI's graphite.

Everything here comes from `build.ts`. Edit that file, then run `make icons`
(needs `bun`, `rsvg-convert` from `brew install librsvg`, and macOS
`iconutil`). Don't edit the generated SVGs by hand.

| File | Use it for |
|---|---|
| `choix-icon.svg`, `png/choix-1024-fullbleed.png` | Full-bleed 1024 × 1024 master with square corners. Use it for the App Store / App Store Connect icon and as a single-layer source in Icon Composer. macOS 26 and the App Store apply the rounded mask. |
| `layers/1-background.svg` … `layers/4-badge.svg` | Separate layers for Xcode's **Icon Composer** (macOS 26 Liquid Glass): background, back prints, front print, badge. They have no baked shadows because Icon Composer adds its own. |
| `choix-icon-macos.svg`, `png/choix-64.png` … `png/choix-1024.png` | The pre-Tahoe macOS template: the rounded shape at 824 px on a 1024 canvas, with a drop shadow. |
| `choix.icns` | A ready-made `.icns` for an app bundle's `CFBundleIconFile`. |
| `choix-icon-small.svg`, `png/choix-16.png`, `png/choix-32.png` | Simplified artwork for 16 and 32 px (one back print, a bigger badge, no fine detail). The `.icns` uses it for its 16 and 32 px sizes. |
| `favicon.svg` | The simplified artwork, filling its canvas. `internal/ui/web/scripts/build.ts` copies it into the SPA as the favicon and the AppBar mark. |
