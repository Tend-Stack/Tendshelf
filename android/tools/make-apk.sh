#!/usr/bin/env bash
# Builds the release APK (plan A10). Usage: android/tools/make-apk.sh [version]   (make apk calls it)
# The version is YEAR.RELEASE.FIX: the argument, else the Git tag this commit is exactly on, else a development build
# (0.0.0-dev, versionCode 1). The APK is signed only when TS_ANDROID_KEYSTORE, TS_ANDROID_KEYSTORE_PASSWORD,
# TS_ANDROID_KEY_ALIAS and TS_ANDROID_KEY_PASSWORD are all set; otherwise it stays unsigned and is named so. Never the debug key.
# Output: android/build/dist/tendshelf-<version>[-unsigned].apk and a .sha256 beside it.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
sdk=${ANDROID_HOME:?ANDROID_HOME is not set}
version=${1:-$(git -C "$root" describe --tags --exact-match 2>/dev/null | sed 's/^v//' || true)}
args=()
[ -n "$version" ] && args=(-PtsVersion="$version")
name=${version:-0.0.0-dev}
signed=1
for v in TS_ANDROID_KEYSTORE TS_ANDROID_KEYSTORE_PASSWORD TS_ANDROID_KEY_ALIAS TS_ANDROID_KEY_PASSWORD; do
  [ -n "${!v:-}" ] || signed=0
done
(cd "$root/android" && ./gradlew --no-daemon assembleRelease "${args[@]}")
out="$root/android/app/build/outputs/apk/release"
dist="$root/android/build/dist"
mkdir -p "$dist"
bt=$(ls -d "$sdk"/build-tools/* | sort -V | tail -1)
if [ "$signed" = 1 ]; then
  src="$out/app-release.apk"
  dest="$dist/tendshelf-$name.apk"
  cp "$src" "$dest"
  "$bt/apksigner" verify -v --print-certs "$dest" | grep -E 'Verifies|certificate (DN|SHA-256)'
else
  src="$out/app-release-unsigned.apk"
  dest="$dist/tendshelf-$name-unsigned.apk"
  cp "$src" "$dest"
  echo "UNSIGNED: no complete TS_ANDROID_* key set; this APK cannot be installed until it is signed (docs/android.md)."
fi
"$root/android/tools/check-release.sh" "$src"
(cd "$dist" && sha256sum "$(basename "$dest")" > "$(basename "$dest").sha256")
"$bt/aapt2" dump badging "$dest" | grep -E "^package:|targetSdkVersion|application-debuggable" || true
ls -l "$dest"
