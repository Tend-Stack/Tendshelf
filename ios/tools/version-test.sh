#!/usr/bin/env bash
# Checks ios/tools/version.sh against a table (decision 0048, I4d). Runs anywhere; needs no Mac.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
failures=0

# ok <expected build number> <version> [suffix]
ok() {
  local want=$1 version=$2 suffix=${3:-} got
  got=$(TS_IOS_BUILD_SUFFIX=$suffix "$here/version.sh" "$version" | sed -n 's/^TS_BUILD_NUMBER=//p') || got=ERROR
  if [ "$got" = "$want" ]; then
    echo "$version${suffix:+ +$suffix} -> $got"
  else
    echo "FAIL: $version${suffix:+ +$suffix} gave '$got', wanted '$want'"
    failures=$((failures + 1))
  fi
}

# bad <version> [suffix]: must exit 2
bad() {
  local version=$1 suffix=${2:-} status=0
  TS_IOS_BUILD_SUFFIX=$suffix "$here/version.sh" "$version" >/dev/null 2>&1 || status=$?
  if [ "$status" -eq 2 ]; then
    echo "'$version'${suffix:+ +$suffix} refused"
  else
    echo "FAIL: '$version'${suffix:+ +$suffix} exited $status, wanted 2"
    failures=$((failures + 1))
  fi
}

ok 202603000 2026.3.0
ok 202613000 2026.13.0
ok 202608001 2026.8.1
ok 202603000.2 2026.3.0 2
bad 2026.0.1
bad 2026.100.0
bad 26.1.0
bad 2026.1.1000
bad 2026.3.0 0
bad 2026.3.0 100

if [ "$failures" -ne 0 ]; then
  echo "version-test: $failures FAILED"
  exit 1
fi
echo "version-test: ok"
