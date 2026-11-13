# Signing checklist

What the repository owner needs to sign, notarize and ship Choix, and which variable each item maps to. The Taskfile
reads these variables from the environment only; none of them is ever written to the repository, and `task build`,
`task test`, `task lint` and `task package` never read one.

A signing task started without one of its variables stops before doing anything, exits 1 and prints
`<task> needs <VAR> (<what it is>). See docs/release/signing-checklist.md.`

## What you need

| # | Item | Where it comes from | Used by |
|---|---|---|---|
| 1 | A paid **Apple Developer Program** membership (a team) | developer.apple.com → Account → Membership | everything below |
| 2 | A **Developer ID Application** certificate, with its private key, in the login keychain | Certificates, Identifiers & Profiles → Certificates → Developer ID Application | `sign:developer-id`, `dmg` |
| 3 | A **notarytool keychain profile** (App Store Connect API key or Apple ID with an app-specific password, stored in the keychain) | `xcrun notarytool store-credentials <profile-name>` | `notarize`, `notarize:dmg` |
| 4 | An **Apple Distribution** (Mac App Distribution) certificate, with its private key, in the login keychain | Certificates → Apple Distribution | `package:mas` |
| 5 | A **Mac Installer Distribution** certificate, with its private key, in the login keychain | Certificates → Mac Installer Distribution | `package:mas` |
| 6 | An explicit **App ID** for `tech.inkhorn.choix` and an **App Store Connect app record** for it (platform macOS) | Identifiers → App IDs; App Store Connect → Apps → New App | `package:mas` |
| 7 | A **Mac App Store provisioning profile** (distribution, macOS) for that App ID, downloaded as a `.provisionprofile` file | Profiles → Mac App Store Connect | `package:mas` |

`security find-identity -v -p codesigning` lists the signing identities in your keychains (items 2 and 4); item 5 is
an installer identity and is listed by `security find-identity -v` without `-p codesigning`.

## Variables

| Variable | What it must contain | Read by |
|---|---|---|
| `CHOIX_SIGN_IDENTITY` | The Developer ID Application identity (item 2): its full name as `security find-identity` prints it, `"Developer ID Application: <Name> (<TEAMID>)"`, or its SHA-1 hash | `sign:developer-id` (required), `dmg` (optional: the disk image is signed when it is set) |
| `CHOIX_NOTARY_PROFILE` | The name given to `xcrun notarytool store-credentials` (item 3); the credentials stay in the keychain | `notarize`, `notarize:dmg` |
| `CHOIX_MAS_APP_IDENTITY` | The Apple Distribution identity (item 4), `"Apple Distribution: <Name> (<TEAMID>)"` or its SHA-1 hash | `package:mas` |
| `CHOIX_MAS_INSTALLER_IDENTITY` | The Mac Installer Distribution identity (item 5), `"3rd Party Mac Developer Installer: <Name> (<TEAMID>)"` or `"Mac Installer Distribution: …"`, as listed | `package:mas` |
| `CHOIX_MAS_PROFILE` | The path of the provisioning profile file (item 7) | `package:mas` |
| `CHOIX_TEAM_ID` | The team's 10-character Team ID (capital letters and digits), shown under Membership | `package:mas` (rendered into `entitlements.mas.plist`) |

Set them in your shell for the release session only, for example `export CHOIX_SIGN_IDENTITY='Developer ID Application: …'`.
Do not put them in a committed file.

## Direct distribution (Developer ID, disk image)

The same tasks run locally and in the release workflow; with the two variables above set, `task package
sign:developer-id notarize dmg notarize:dmg verify:release VERSION=X.Y.Z` is the whole release minus the upload. In
order:

1. `task package` — builds `bin/Choix`, assembles `bin/Choix.app` (no bundled library) and signs it ad hoc. Needs no
   variable.
2. `task sign:developer-id` — re-signs the app with `CHOIX_SIGN_IDENTITY`, `--options runtime --timestamp` and
   `build/darwin/entitlements.plist` (the four sandbox entitlements).
3. `task notarize` — zips the app, submits it with `xcrun notarytool submit --wait --keychain-profile
   "$CHOIX_NOTARY_PROFILE"`, fails unless Apple answers `Accepted` (printing the notary log when it does not), and
   staples the ticket to `bin/Choix.app`.
4. `task dmg` — writes `bin/Choix-<version>-arm64.dmg` holding `Choix.app` and an `Applications` link (mounted once to
   check both), signed with `CHOIX_SIGN_IDENTITY` when it is set. Without the variable it makes the same image,
   unsigned, for local tests.
5. `task notarize:dmg` — notarizes the signed disk image the same way and staples its ticket.
6. `task verify:release` — fails unless `codesign --verify --deep --strict` accepts the app, `spctl --assess --type
   exec` accepts it and `spctl --assess --type open --context context:primary-signature` accepts the disk image, both
   as `source=Notarized Developer ID`, and `xcrun stapler validate` accepts both. It fails closed on `spctl
   --status`: only an explicit `assessments disabled` (spctl then accepts anything) skips the verdict check, with a
   warning, and the file's stapled ticket must still pass `stapler validate`; any other answer, or a failing `spctl
   --status`, fails the task. It prints only the verdict and its `source=`, never spctl's `origin=` line, so no
   signer name or Team ID reaches the (public) release log; the notary output is redacted the same way.

`VERSION` is the version without the tag's `v` (`2.1.0`); the release workflow passes the tag's. Without it the
Taskfile uses `git describe`, which is fine for a rehearsal.

### The hardened runtime

`sign:developer-id` signs with `--options runtime --timestamp`; `sign:dev` signs ad hoc without `--options runtime`.
Turning the hardened runtime on for ad hoc builds is a pending follow-up. The release workflow builds, notarizes and
publishes the hardened, Developer ID–signed build, and `verify:release` checks its signature and tickets. The
workflow does not launch it, so after a release check that `CHOIX_SELFCHECK=1 /Applications/Choix.app/Contents/MacOS/Choix`
reports `"sandboxed":true`, `"featurePrint":"ok"` and `"quality":"ok"` on the installed app.

## Release workflow (CI)

`.github/workflows/release.yml` runs the tasks above on GitHub's Apple Silicon runners.

**When it runs.**

- A pushed tag `v<major>.<minor>.<patch>` with major 2 or later (`v2.0.0`, `v2.1.3`, `v10.0.0`; never the old
  `v0.1.x` tags, nor `v2.0.0-beta.1`): a release. Every secret below must be set; one that is missing fails the first
  job, naming it, before anything is built, signed or published.
- **Run workflow** on the Actions tab (`workflow_dispatch`): a dry run. It builds the ad hoc signed app (artifact
  `Choix-app`) and, when the five signing secrets are set, signs and notarizes it and makes the notarized disk image
  (artifact `Choix-dmg`). It never creates a release and never touches the tap. Without the signing secrets (as in
  a fork) it only builds, and the run summary says signing was skipped.

**What a release run does.** `gate` checks the tag and the secrets; `build` runs `task package VERSION=<version>`;
`sign` imports the certificate into a throwaway keychain, takes the first valid "Developer ID Application" identity
in it as `CHOIX_SIGN_IDENTITY` (its SHA-1 hash, masked with `::add-mask::`; no name or Team ID is written to the log), stores the API key there
as the notarytool profile `choix-release` (`CHOIX_NOTARY_PROFILE`), runs `task sign:developer-id notarize dmg
notarize:dmg verify:release`, and deletes the keychain, the `.p12` and the `.p8` at the end whatever happened;
`release` runs `gh release create v<version> Choix-<version>-arm64.dmg --title "Choix <version>" --generate-notes`
(the only job that can write to the repository); `homebrew` calls the tap's reusable workflow
(`volodymyrsmirnov/homebrew-tap/.github/workflows/update.yml@main`), which writes `Casks/choix.rb` with the new
version and SHA-256 and pushes it.

**Repository secrets** (Settings → Secrets and variables → Actions). The names are the ones
`volodymyrsmirnov/diriger` uses, so both repositories are set up the same way and can share the values.

| Secret | What it must contain | How to make it |
|---|---|---|
| `MACOS_CERTIFICATE` | The Developer ID Application certificate with its private key, as a `.p12`, base64-encoded | Keychain Access → My Certificates → select the certificate (with its key) → Export → `.p12` with a password; then `base64 -i DeveloperID.p12 \| gh secret set MACOS_CERTIFICATE` |
| `MACOS_CERTIFICATE_PWD` | The `.p12`'s export password | `gh secret set MACOS_CERTIFICATE_PWD` (prompts) |
| `APPSTORE_KEY_ID` | An App Store Connect API key's ID | App Store Connect → Users and Access → Integrations → App Store Connect API → Team Keys → generate a key (the Developer role is enough to notarize); its Key ID is listed with it |
| `APPSTORE_ISSUER_ID` | That team's issuer ID (a UUID) | The same page, above the keys |
| `APPSTORE_PRIVATE_KEY` | The key's `AuthKey_<id>.p8`, base64-encoded | The `.p8` downloads once, when the key is made; `base64 -i AuthKey_<id>.p8 \| gh secret set APPSTORE_PRIVATE_KEY` |
| `TAP_GITHUB_TOKEN` | A token that can push to `volodymyrsmirnov/homebrew-tap` (already set) | A fine-grained personal access token for that repository with Contents: read and write |

Delete the exported `.p12` and the `.p8` from disk once the secrets are set (keep the `.p8` somewhere safe if you
want it for local notarization: Apple does not let you download it again). The Mac App Store package is not built in
CI: `task package:mas` runs locally with the variables above.

**Rehearsing a release locally,** with the certificate in your login keychain:

1. Once: `xcrun notarytool store-credentials choix-notary --key AuthKey_<id>.p8 --key-id <key id> --issuer <issuer
   id>` (the same API key as the secrets; it checks the credentials with Apple).
2. `export CHOIX_SIGN_IDENTITY=<SHA-1 from security find-identity -v -p codesigning> CHOIX_NOTARY_PROFILE=choix-notary`
3. `task package sign:developer-id notarize dmg notarize:dmg verify:release VERSION=2.0.0`
4. `CHOIX_SELFCHECK=1 bin/Choix.app/Contents/MacOS/Choix` reports `"sandboxed":true`, `"featurePrint":"ok"` and
   `"quality":"ok"`;
   open `bin/Choix-2.0.0-arm64.dmg`, drag the app to Applications and launch it once.
5. Then a dry run of the workflow (Actions → Release → Run workflow) proves the secrets: its `Choix-dmg` artifact is
   the notarized image. Nothing is published by either.

## Mac App Store

The App Store Connect record and its listing text are in [`app-store.md`](app-store.md).

1. `task package:mas` — builds, assembles `bin/Choix.app`, copies `CHOIX_MAS_PROFILE` to
   `Contents/embedded.provisionprofile`, renders `build/darwin/entitlements.mas.plist.tmpl` with `CHOIX_TEAM_ID`
   (the four sandbox entitlements plus the application and team identifiers), signs the app with
   `CHOIX_MAS_APP_IDENTITY`, and writes `bin/Choix-<version>.pkg` with `productbuild --component bin/Choix.app
   /Applications --sign "$CHOIX_MAS_INSTALLER_IDENTITY"`.
2. Upload the `.pkg` to App Store Connect with Apple's Transporter app, then submit the build for review there.

App Store Connect needs a numeric version, so `package:mas` takes it from the last release tag (`vX.Y.Z` at or before
`HEAD`, without the `v` and `git describe`'s suffix) and stops when there is none; the build number is the commit
count, which only grows. The MAS-signed `bin/Choix.app` is for upload, not for running locally: run `task package`
again for a local build.

## Never

- Never commit a certificate, private key, provisioning profile, API key, keychain profile secret or Team ID.
- Never add a temporary-exception entitlement or `com.apple.security.cs.disable-library-validation`: the entitlements
  are exactly the four in `build/darwin/entitlements.plist` (development and Developer ID builds), and for the Mac App
  Store those four plus the application and team identifiers in `build/darwin/entitlements.mas.plist.tmpl`.
- Never sign with `--deep`. The bundle holds no nested code to sign; if one is ever added, sign it on its own, before
  the app that seals it.
