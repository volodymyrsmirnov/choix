# Release checklist

What to do, in order, to release Choix. The details live in the documents this
list points to; nothing here replaces them.

## Once: set up signing

The secrets were in place for 2.0.0, the first release. Repeat a step only
when what it sets up changes, for example a renewed certificate or a new API
key or token.

1. Get the certificates, the notary credentials and the App Store items listed
   in [`docs/release/signing-checklist.md`](release/signing-checklist.md).
2. Add the six repository secrets listed there under "Release workflow (CI)":
   `MACOS_CERTIFICATE`, `MACOS_CERTIFICATE_PWD`, `APPSTORE_KEY_ID`,
   `APPSTORE_ISSUER_ID`, `APPSTORE_PRIVATE_KEY` (the names diriger uses) and
   `TAP_GITHUB_TOKEN` (already set). A release tag with one missing fails
   before anything is signed or published.
3. Rehearse once: locally (the signing checklist's "Rehearsing a release
   locally"), then with a dry run of the workflow (Actions → Release → Run
   workflow), which signs and notarizes but publishes nothing.

## Before tagging

1. `main` is green in CI: the `ci`, `design` and `parity` jobs of
   `.github/workflows/ci.yml`.
2. `task design:status` shows every state approved and no finding.
3. `task uat` passes on clones of the real archive
   ([`docs/release/uat.md`](release/uat.md)). Paste only what that document
   allows: `folder-K` labels, extensions and numbers.
4. `task smoke:sandbox` passes, including its manual checks
   ([`docs/release/sandbox-smoke.md`](release/sandbox-smoke.md)).

## Direct distribution (GitHub release and Homebrew cask)

1. Tag the release commit `v<major>.<minor>.<patch>` with major 2 or later (no
   suffix: `v2.0.0-beta.1` does not trigger a release) and push the tag:

   ```bash
   git tag v<version>
   git push origin v<version>
   ```

2. The Release workflow (`.github/workflows/release.yml`) runs `gate` (the tag
   and the secrets), `build` (`task package`), `sign` (`task sign:developer-id
   notarize dmg notarize:dmg verify:release`), `release` (the GitHub release
   `v<version>` with `Choix-<version>-arm64.dmg` and generated notes) and
   `homebrew` (the tap's `Casks/choix.rb` with the new version and SHA-256).
   If a job fails, its log and the run summary say why; nothing after it runs.
   A failed `homebrew` job can be re-run alone once the release exists.
3. Download the disk image from the release, open it on a Mac with Apple
   Silicon, drag Choix to Applications and launch it once: the welcome panel
   shows, a folder can be added and scanned.
4. `brew update && brew install --cask volodymyrsmirnov/tap/choix` (or
   `brew upgrade --cask choix`) installs the new version.

## Mac App Store

1. With the variables of the signing checklist set, run `task package:mas` on
   the tagged commit. It writes `bin/Choix-<version>.pkg`; the version comes
   from the last `vX.Y.Z` tag and the build number from the commit count.
2. Upload the package with Apple's Transporter app and submit the build for
   review in App Store Connect
   ([`docs/release/signing-checklist.md`](release/signing-checklist.md), "Mac App Store").
