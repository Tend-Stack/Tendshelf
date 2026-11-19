# iPhone app: building on a Mac

Decision [0048](decisions/0048-iphone-app.md), plan [iphone-app.md](plans/iphone-app.md). Phase I1a (this page's scope): the
shared Kotlin module builds for iOS, the server treats `ts_shell=ios` as a phone app, and the Mac scripts exist. The Swift
shell comes in I1b and I1c. Nothing here is part of `make check` or `make build`; Linux and CI never touch an iOS target.

## What builds where

- `android/shared` is a Kotlin Multiplatform module with `jvm()` always, and `iosArm64()` and `iosSimulatorArm64()` only when
  Gradle runs on a Mac (`os.name` starts with `Mac`). Both iOS targets go into one static XCFramework, `TendshelfShared`.
- On the Mac Gradle runs with `-Ptendshelf.sharedOnly=true`, which leaves `:app` out of the build, so no Android SDK is
  needed there.
- `commonMain` stays platform-neutral: no `java.*` or `javax.*` import and no `Math.*` (`android/tools/check-android.sh`
  fails on them). Platform pieces sit behind `expect`/`actual`: `parseRawUri` (`java.net.URI` on the JVM, `NSURLComponents`
  on iOS, held to the same answers by `RawUriTest`, which runs on both) and `SynchronizedOnJvm` (a lock on the JVM, nothing
  on iOS, where the app calls an `Outbox` from one serial queue).

## Scripts

All three are in `ios/tools/`.

- `ios-build.sh [shared|build|test|all|clean]` runs on the Mac (CI will call it). Steps: check Xcode's first-launch
  components (exit 3 if missing), pick JDK 21 with `/usr/libexec/java_home -v 21`, find the simulator's UDID (exit 4 if
  none). `shared` runs `:shared:iosSimulatorArm64Test` and `:shared:assembleTendshelfSharedDebugXCFramework`; `build` and
  `test` run `xcodebuild` on `ios/Tendshelf.xcodeproj` (exit 5 until I1b adds it). Logs go to `ios/build/logs/`, result
  bundles to `ios/build/results/` (newest three kept). `clean` removes `ios/build` and `android/shared/build`.
- `mac-build.sh [step]` runs from Linux: it syncs the tree to the Mac with `rsync` (excludes in `rsync-exclude`), takes a
  lock (`<dir>.lock`, exit 6 when another build holds it), runs `ios-build.sh` over `ssh` and prints its output.
  `mac-build.sh clean` also removes the synced tree on the Mac. It needs `TS_MAC_HOST` (exit 2 otherwise).
- `make ios-shared` is `mac-build.sh shared`; `make ios-sim` is `mac-build.sh all`.

| Variable | Meaning | Default |
| --- | --- | --- |
| `TS_MAC_HOST` | the Mac's ssh host (key login, `BatchMode`) | required |
| `TS_MAC_DIR` | directory on the Mac, relative to its home | `tendshelf-ios` |
| `TS_IOS_SIM` | simulator name | `iPhone 17` |
| `TS_IOS_SIM_OS` | iOS runtime of the simulator | newest installed |
| `TS_BUILD_DESCRIBE` | version string baked into the build | `git describe --tags --long --dirty`, else `unknown` |

The Gradle property `tendshelf.iosSimulator` (set by `ios-build.sh` to the UDID) names the simulator that the shared
Kotlin/Native tests boot; it falls back to `iPhone 17`.

## First launch

A fresh Xcode needs `sudo xcodebuild -runFirstLaunch` once (it installs components that need administrator rights).
`xcodebuild -checkFirstLaunchStatus` exits 0 afterwards, and `ios-build.sh` refuses to run until it does. The shared Kotlin
build and its simulator tests ran on a Mac where it still exited 69; `xcodebuild` for the Swift shell is what needs it.
`xcrun` prints `Install Failed: Authorization is required` lines until then; they are harmless noise.

## What is pinned

Gradle checks every dependency against `android/gradle/verification-metadata.xml`, which only gained entries for the iOS
artifacts of kotlinx-serialization and the Kotlin/Native distribution `kotlin-native-prebuilt` (so the compiler itself is
pinned by sha256). Kotlin/Native then downloads its own helper packages (LLVM and libffi) from
`download.jetbrains.com/kotlin/native` into `~/.konan/dependencies` (about 1.5 GB with the compiler). Those are not covered
by Gradle's verification and carry no checksum in Kotlin's own configuration: they are the one unpinned download of the iOS
build, fetched at build time on the Mac only, never at runtime and never in the shipped app.

## Cleanup

`make ios-shared` leaves the synced tree and its build output on the Mac. `ios/tools/mac-build.sh clean` removes that tree;
`ios/tools/ios-build.sh clean` (on the Mac) removes only the build output. `~/.konan` is a cache Gradle reuses and is kept.
