#!/usr/bin/env bash
# Reproducibility check (plan A10 D3, invariant 3): the same source built twice, in two different folders with two
# different Gradle homes and the same SOURCE_DATE_EPOCH, must give the same unsigned APK. Usage:
#   android/tools/repro-check.sh [version]      (needs ANDROID_HOME and network for the second Gradle home)
# The source is the current tree (tracked files and new files that are not ignored), copied to each folder, so it also
# works before a commit. On a difference it lists the entries that differ and exits 1; nothing is patched over.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
: "${ANDROID_HOME:?ANDROID_HOME is not set}"
version=${1:-2026.1.0}
epoch=$(git -C "$root" log -1 --format=%ct)
work=${REPRO_DIR:-$root/.local/tests/android/repro/$(date -u +%Y%m%dT%H%M%SZ)}
mkdir -p "$work"
trap 'rm -rf "$work"' EXIT
build() { # build <label>
  local dir="$work/$1/src"
  mkdir -p "$dir"
  (cd "$root" && git ls-files -co --exclude-standard -z | tar --null -T - -cf -) | tar -xf - -C "$dir"
  (cd "$dir/android" && env -u TS_ANDROID_KEYSTORE -u TS_ANDROID_KEYSTORE_PASSWORD -u TS_ANDROID_KEY_ALIAS -u TS_ANDROID_KEY_PASSWORD \
    SOURCE_DATE_EPOCH="$epoch" GRADLE_USER_HOME="$work/$1/gradle-home" ./gradlew --no-daemon -q assembleRelease -PtsVersion="$version" > "$work/$1/build.log" 2>&1) \
    || { echo "build $1 failed:"; tail -n 20 "$work/$1/build.log"; exit 2; }
  cp "$dir/android/app/build/outputs/apk/release/app-release-unsigned.apk" "$work/$1.apk"
}
build a
build b
sa=$(sha256sum "$work/a.apk" | cut -d' ' -f1)
sb=$(sha256sum "$work/b.apk" | cut -d' ' -f1)
echo "a: $sa"
echo "b: $sb"
if [ "$sa" = "$sb" ]; then echo "reproducible: identical unsigned APKs"; exit 0; fi
echo "DIFFERENT. Entries that differ (unzip -v, name and CRC):"
diff <(unzip -v "$work/a.apk" | awk '{print $NF, $7}') <(unzip -v "$work/b.apk" | awk '{print $NF, $7}') || true
exit 1
