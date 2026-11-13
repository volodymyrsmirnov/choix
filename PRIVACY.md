# Choix privacy policy

Last updated: 13 November 2026

Choix is a photo-and-video culling app for macOS, published on the Mac App Store by
INKHORN TECH - FZCO and on GitHub by Volodymyr Smirnov.

## What Choix collects

Nothing. Choix makes no network request: it has no account, no analytics, no crash reporting,
no advertising and no third-party SDK. No data about you or your photos is sent to us or to
anyone else.

## What Choix does on your Mac

- It reads the photos and videos in the folders you add, to show them, group them and rate
  their technical quality. Grouping uses Apple's Vision framework, and the quality rating is a
  formula built into the app; both run on your Mac.
- It stores its own data on your Mac: its settings and the list of folders you added (in the
  app's sandbox container), and, inside each folder you added, a `.choix` working folder
  (thumbnails and a small database) and the `picks` folder holding copies of the photos you
  picked.
- It never changes, moves or deletes your original files.

Removing a folder from the Library forgets it. Deleting the app's container, the `.choix`
folders and the `picks` folders removes everything Choix stored.

## Contact

Questions about this policy: open an issue at
<https://github.com/volodymyrsmirnov/choix/issues>.
