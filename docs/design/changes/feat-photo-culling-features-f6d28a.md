---
branch: feat/photo-culling-features-f6d28a
states: [202-213, 215, 217-227, 229, 231, 500-504, 507-509, 512-513, 518-520, 603, 605, 702-703, 803, 805, 807]
retired: [11-17, 112, 120, 216, 228, 505, 506, 514-517]
ui: true
---

Removes the model download UI: visual grouping now uses Apple's on-device
image analysis and the quality rating is a built-in formula, so nothing is
downloaded. Nothing new is added.

Retired: the first-launch welcome panel (11–17), the Library's compact
download banner (112, 120), the Folder page's time-only notices about a
model download (216, 228), and the Settings model cards, download states
and quality error (505, 506, 514–517). An empty Library is the existing drop
zone (100) as soon as the list has loaded.

Changed: Settings loses the two model cards; the Visual grouping, Visual
similarity and Quality rating hints and the "off" status line carry the new
copy; the default similarity is 0.90. The Folder states and the two native
replicas drawn over the Folder page re-hash because `FolderScreen.tsx` is
hashed into them (its time-only notice is gone), most without a visible
change. Gallery: 803 shows the slider at the new default, 805 the quality
indicator's rating pass only and two of the page's real banners in place
of the download banners, 807 a Folder title bar rating quality in place of
the quality model download.

Overlays: the sample info toast in 603 and 605 announced that visual
grouping was ready, which nothing announces any more; it now shows a toast
the app really shows, a folder (“Film scans”) that left the Library.
