# Tendshelf for Android: build, install, sign, upgrade

The Android app lives in `android/` (decision 0024). This page is for the person who builds or installs it. Nothing
here needs an account with any store, and nothing in the repository holds a key.

## Build

You need a JDK 21 and the Android SDK (platform 36, build-tools 36.0.0), with `ANDROID_HOME` set. No Android Studio.

```
make apk-debug     # android/app/build/outputs/apk/debug/app-debug.apk, signed with Android's debug key
make apk           # the release APK, see below
```

`make apk` runs `android/tools/make-apk.sh`. The version is the Git tag the commit is exactly on (`v2026.3.0` gives
versionName `2026.3.0`); on any other commit it is a development build, `0.0.0-dev`, versionCode 1. The versionCode is
`year * 100000 + release * 1000 + fix` (2026.3.0 is 202603000), so it rises with every release. To build a version
without the tag: `android/tools/make-apk.sh 2026.3.0`. A version that is not `YEAR.RELEASE.FIX` fails the build.

The output is `android/build/dist/tendshelf-<version>.apk` (signed) or `tendshelf-<version>-unsigned.apk`, with a
`.sha256` file beside it. The script then runs `android/tools/check-release.sh`, which fails if any debug hook, test
address or sign-in shortcut is in the APK.

## Signing

The release APK is signed only when all four environment variables are set; otherwise it is left unsigned and says so.
It is never signed with the debug key. An unsigned APK cannot be installed.

| Variable | Meaning |
| --- | --- |
| `TS_ANDROID_KEYSTORE` | path of your keystore file |
| `TS_ANDROID_KEYSTORE_PASSWORD` | the keystore password |
| `TS_ANDROID_KEY_ALIAS` | the key's name inside the keystore |
| `TS_ANDROID_KEY_PASSWORD` | the key's password |

### Make your own key

Do this once, on your own computer, and keep the file and the passwords somewhere safe and backed up. If you lose the
key you can never update an installed app: every phone would have to uninstall first.

```
keytool -genkeypair -keystore tendshelf-release.jks -alias tendshelf -keyalg RSA -keysize 4096 -validity 10000
```

`keytool` comes with the JDK and asks for the passwords and a name. Never put the file in the repository (`android/.gitignore`
ignores `*.jks`, `*.keystore`, `*.p12` and `*.pfx`, but keep it elsewhere anyway). Then:

```
export TS_ANDROID_KEYSTORE=$HOME/keys/tendshelf-release.jks
export TS_ANDROID_KEYSTORE_PASSWORD=...   # typed by you, not saved in a file in the repository
export TS_ANDROID_KEY_ALIAS=tendshelf
export TS_ANDROID_KEY_PASSWORD=...
make apk
```

The script prints `Verifies` and the certificate's SHA-256. Write that fingerprint down: it is how anyone, including a
store, checks that a later APK came from you. `apksigner verify -v --print-certs <apk>` (in `build-tools`) shows it again.

### Signing in CI

The tag workflow (`.gitea/workflows/release.yml`, job `apk`) runs after the server release and cannot hold it back.
Add these repository secrets, with the keystore as base64 (`base64 -w0 tendshelf-release.jks`): `TS_ANDROID_KEYSTORE_B64`,
`TS_ANDROID_KEYSTORE_PASSWORD`, `TS_ANDROID_KEY_ALIAS`, `TS_ANDROID_KEY_PASSWORD`. Add two repository variables (not
secrets): `TS_ANDROID_CMDLINE_TOOLS_URL` and `TS_ANDROID_CMDLINE_TOOLS_SHA256`, the Android command-line tools archive from
developer.android.com and its SHA-256, so the build does not trust a download it cannot check. Without the variables the job
says it skipped the Android build. Without the secrets it builds and attaches an unsigned APK named `-unsigned.apk` and
says so in its log; the server release is unaffected either way. The keystore is decoded to a temporary file and removed
at the end of the job.

## Install

1. Copy the signed APK to the phone (or download it from the release page) and open it. Android asks once to allow
   installs from that app (Files or the browser).
2. Open Tendshelf, type the address of your server and sign in on the server's own page.

From a computer: `adb install tendshelf-2026.3.0.apk`.

On the first screen choose "I have a server" (type its address) or "I have an invitation" (paste the link you were sent). A link
that opens the app from a browser's "Open in the app" button, or from `tendshelf://open?link=...`, goes the same way: the phone
shows the exact address in a dialog ("Connect to this server?") and connects only after Connect. A link holds no password or
code. A phone that is already signed in to that server refuses the link; for another server it asks before leaving the first
one (decision [0028](decisions/0028-qr-sign-in.md)).

## Upgrade

Install the new APK over the old one: downloads, places and the sign-in stay. This works only when both APKs are signed
with the same key and the new versionCode is higher (it always is for a later release).

**The first release needs an uninstall.** A test build made by `make apk-debug` is signed with the debug key. A release
APK is signed with your key, and Android refuses to update an app across two keys. So on a phone that has the debug build:
uninstall Tendshelf (its downloads and sign-in go with it), then install the release. After that, upgrades keep everything.
Going back from release to debug needs an uninstall the same way.

## Check that two builds match

`android/tools/repro-check.sh` builds the unsigned APK twice, in two folders with two Gradle homes, and compares the
SHA-256. Last run (2026-11-06): identical. If they ever differ it lists the entries that differ and exits 1. This
needs the same JDK and SDK build-tools on both sides, which is why the CI job pins them. `android/tools/version-code-check.sh`
checks the versionCode rule.

## Store metadata

`android/fastlane/metadata/android/en-US/` holds the title, descriptions and a changelog per versionCode, in the layout
F-Droid reads and Play tools accept. `android/fdroid/app.tendshelf.android.yml` is the F-Droid recipe: the GitHub source,
the release certificate's SHA-256 as the one allowed signing key, and `Binaries:` pointing at the APK on each GitHub
release, so F-Droid publishes the operator-signed APK when its own build from the tag matches it. The channel is F-Droid
(operator, 2026-11-07). Screenshots and a 512 px icon PNG are not included yet (they need a synthetic library on a device).

To submit, from the operator's own GitLab account: fork `https://gitlab.com/fdroid/fdroiddata`, copy the recipe to
`metadata/app.tendshelf.android.yml`, and open a merge request titled "New app: Tendshelf". The F-Droid pipeline on that
request builds the tag; read its log for scanner findings or a mismatch with the release APK. Each later release needs
only its tag, its GitHub APK and a `changelogs/<versionCode>.txt` (`UpdateCheckMode: Tags` finds new versions).

## Read aloud

The reader speaks the book through the phone's own text-to-speech engine (`android.speech.tts.TextToSpeech`, platform API, no new dependency;
decision [0032](decisions/0032-read-aloud.md)). Android System WebView has no usable `speechSynthesis`, so `native-speech.js` (loaded only when
`data-shell="android"`) is the engine `reader-aloud.js` expects, and `Speech.kt` is its other half.

- Page to app, three new bridge words (protocol version 1, additive; an older app drops them and the page shows no control):
  `tts voices`, `tts stop` and `say <n> <rate> <voiceId> <text>`. `n` is 1 to 9 digits without a leading zero, `rate` the `speed` form
  (`d.dd`, 0.50 to 3.00 in steps of 0.05), `voiceId` the engine's voice name (the usual id charset, with a letter or digit).
- `say` is the first page message that carries free text, the sentence to speak: 1 to 4,000 UTF-16 units, no control character (below U+0020 or
  U+007F to U+009F), no unpaired surrogate. It goes only to the speech engine; the log gets its length, and nothing is echoed. Every other message keeps
  the 128-character ASCII rule.
- App to page: `{"t":"voices","list":[{"id","lang","def"}]}` (at most 200, empty when the engine failed to start) and
  `{"t":"tts","n":1,"ev":"start|end|error|stopped"}`. `stopped` is sent when the app stops speech by itself.
- Only voices that need no network and are installed are listed or used; a `say` that names any other voice is answered with `error`.
- Speech runs while the app is in front: `onStop`, a new document, sign-out and `onDestroy` stop it; the reader keeps its place.
- The `<queries>` entry for `android.intent.action.TTS_SERVICE` lets the app bind the engine on targetSdk 36.

## Checks before a release

- `android/tools/check-android.sh` runs `:shared:test` and `lintRelease`. `.gitea/workflows/android.yml` runs it on every push that touches `android/`, and `make-apk.sh` runs it before the tag build. Both need the two `TS_ANDROID_CMDLINE_TOOLS_*` variables; without them the job says it was skipped, which is not a pass.
- Lint has `NewApi` fatal (minSdk is 28). `app/lint-baseline.xml` holds the older findings of other kinds; never add a `NewApi` entry, fix the call.
- The app sends the token, and acts on a 401 or 404, only to an address whose `/healthz` says `app: tendshelf` with the install id saved at connect time (`ServerGate`).
- The F-Droid recipe names v2026.4.0; F-Droid's own build of the tag must match the GitHub APK (`android/tools/repro-check.sh` checks reproducibility here).

