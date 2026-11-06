#!/usr/bin/env bash
# Checks the versionCode rule (plan A10 D2): strictly rising over releases, and bad versions fail the build.
# Usage: android/tools/version-code-check.sh      (runs Gradle's printVersionCode over a table)
set -euo pipefail
cd "$(dirname "$0")/.."
code() { ./gradlew --no-daemon -q printVersionCode -PtsVersion="$1" 2>/dev/null | sed -n 's/^versionCode=//p'; }
prev=0
for v in 2026.1.0 2026.1.1 2026.2.0 2026.3.0 2027.1.0; do
  c=$(code "$v")
  echo "$v -> $c"
  [ -n "$c" ] && [ "$c" -gt "$prev" ] || { echo "FAIL: $v ($c) does not rise above $prev"; exit 1; }
  prev=$c
done
for bad in 26.1.0 2026.1 2026.0.0 2026.100.0 2026.1.1000 v2026.1.0 2026.1.0-rc1 ""; do
  if [ -n "$(code "$bad")" ]; then echo "FAIL: '$bad' was accepted"; exit 1; fi
  echo "'$bad' rejected"
done
echo "version codes rise and bad versions fail"
