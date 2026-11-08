#!/usr/bin/env bash
# The Android gates that need no phone: the Kotlin unit tests of :shared and Android lint on the release variant
# (NewApi is fatal; app/lint-baseline.xml holds only the older findings of other kinds). Used by .gitea/workflows/android.yml
# and runnable by hand: ANDROID_HOME=... android/tools/check-android.sh
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
: "${ANDROID_HOME:?ANDROID_HOME is not set}"
cd "$root/android"
./gradlew --no-daemon :shared:jvmTest lintRelease
