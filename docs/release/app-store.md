# Mac App Store listing

The App Store Connect record for Choix and the text it carries. Keep this file in step with App
Store Connect when the listing changes. Nothing here is secret; the Team ID, certificates and
provisioning profile stay out of the repository. Only the company, INKHORN TECH - FZCO, is named in
the listing.

## Record

| Field | Value |
|---|---|
| Platform | macOS |
| Name | Choix: Photo Culling ("Choix" alone is taken on the App Store) |
| Subtitle | Pick your best shots, fast |
| Bundle ID | `tech.inkhorn.choix` (explicit App ID, no capabilities) |
| SKU | `choix-macos` |
| Primary language | English (U.S.) |
| Category | Photo & Video; secondary Productivity |
| Price | Free, all 175 countries and regions |
| Age rating | 4+ (every questionnaire answer "No" or "None") |
| Content rights | Contains no third-party content |
| App Privacy | Data Not Collected |
| Privacy policy | <https://inkhorn.tech/privacy-policy/> |
| Support URL | <https://inkhorn.tech/> |
| Marketing URL | none |
| Copyright | 2026 INKHORN TECH - FZCO |
| App Review contact | INKHORN TECH - FZCO, hey@inkhorn.tech (phone in App Store Connect); no sign-in |
| Version | 2.2.0, matching the release tag `v2.2.0` that `task package:mas` stamps into the build |
| Screenshots | Five, 2880 × 1800: Library, cluster grid, Focus, Compare, Settings |

## Promotional text

Walk in with a 2,000-photo trip dump, walk out an hour later with a picks folder ready to edit. Grouping and quality ratings run entirely on your Mac.

## Keywords

```
cull,culling,culler,photo picker,raw,raf,dng,heic,burst,similar,duplicate,select,sort,shoot,keeper
```

## Description

```
Choix gets you from a full memory card to a short list of keepers, fast.

Add the folders from a shoot to your Library. Choix scans them in the background and groups every photo and video by camera, by time and by how alike the frames look, so eight near-identical sunsets sit together and you choose one instead of scrolling past all eight.

HOW IT WORKS
• Add folders with ⌘O or by dropping them on the window. Folders scan in the background, and the one you open goes first.
• Clusters: shots are grouped by camera, then by gaps in time, then by visual similarity, computed on your Mac with Apple's Vision framework.
• Focus: step through a cluster with the arrow keys, pick with Space, reject with X, undo with ⌘Z.
• Compare: put similar frames side by side (C) and pixel-peep at full size (Z).
• Quality rating: an advisory 0–10 score for sharpness, noise and exposure helps you spot the blurred or noisy frame in a burst. It never picks for you.
• Picks are copied into a picks folder inside the shoot, ready for your editor. Originals are never moved, renamed or changed.

FORMATS
RAF and DNG raw files (using the preview the camera embeds), HEIC, JPEG, TIFF and PNG photos, and MOV, MP4 and M4V videos.

PRIVATE BY DESIGN
• Nothing leaves your Mac. Choix makes no network requests, needs no account and collects no data.
• Grouping and the quality rating run on your Mac, with nothing to download.
• Choix runs in the macOS App Sandbox and reaches only the folders you choose.

SAFE WITH YOUR FILES
• Originals are opened read-only.
• Inside a folder, Choix writes only its own .choix working folder and your picks folder.
• A pick is saved to disk before the app shows it as done.
• Removing a folder from the Library only forgets it; nothing on disk is touched.

Requires a Mac with Apple silicon and macOS 14 or later.
```

## Notes for App Review

```
No sign-in is needed: Choix has no accounts and makes no network requests.

To review Choix you need a folder that contains photos or images (JPEG, HEIC, PNG, TIFF, or camera RAW such as DNG); videos (MOV, MP4) work too. Choix works only on folders the user chooses.

1. Launch Choix. The Library is empty.
2. Click Add Folder (⌘O) and choose a folder of photos in the system file picker, or drop the folder onto the window.
3. Choix scans the folder in the background and groups its photos into clusters of similar shots. Open the folder to see them.
4. Select a photo and press Space to pick it (X rejects, ⌘Z undoes). The picked photo is copied into a "picks" subfolder of the chosen folder; File > Open Picks Folder (⌘P) shows it in Finder.

Choix only reads the original files. Inside the chosen folder it writes only its ".choix" working folder (thumbnails and a small database) and the "picks" folder, using the security-scoped bookmark created when the user chose the folder. Removing a folder from the Library only forgets it.
```
