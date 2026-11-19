#!/usr/bin/env bash
# Prints the iPhone app's version and build number as two lines, TS_VERSION=... and TS_BUILD_NUMBER=...
# (decision 0048, I4d). Works on Linux and on the Mac.
#
#   version.sh [VERSION]
#
# VERSION is YEAR.RELEASE.FIX, for example 2026.3.0; without it the exact Git tag of HEAD is used (without the
# "v"), and without a tag this is a development build: 0.0.0 and 1. The build number is Android's versionCode:
# year * 100000 + release * 1000 + fix. TS_IOS_BUILD_SUFFIX (1..99) appends ".<n>" for a second upload of the
# same version (App Store Connect needs a new build number for each upload).
#
# Exit codes: 2 the version or the suffix is not valid.
set -euo pipefail

version=${1:-}
if [ -z "$version" ]; then
  version=$(git describe --tags --exact-match 2>/dev/null || true)
  version=${version#v}
fi

if [ -z "$version" ]; then
  echo "TS_VERSION=0.0.0"
  echo "TS_BUILD_NUMBER=1"
  exit 0
fi

re='^([0-9]{4})\.([0-9]{1,2})\.([0-9]{1,3})$'
if ! [[ $version =~ $re ]]; then
  echo "version must be YEAR.RELEASE.FIX, for example 2026.3.0; got '$version'" >&2
  exit 2
fi
year=$((10#${BASH_REMATCH[1]}))
release=$((10#${BASH_REMATCH[2]}))
fix=$((10#${BASH_REMATCH[3]}))
if [ "$release" -lt 1 ] || [ "$release" -gt 99 ] || [ "$fix" -gt 999 ]; then
  echo "version out of range (release 1..99, fix 0..999): $version" >&2
  exit 2
fi

build=$((year * 100000 + release * 1000 + fix))
if [ -n "${TS_IOS_BUILD_SUFFIX:-}" ]; then
  sre='^[0-9]{1,2}$'
  if ! [[ $TS_IOS_BUILD_SUFFIX =~ $sre ]] || [ "$((10#$TS_IOS_BUILD_SUFFIX))" -lt 1 ]; then
    echo "TS_IOS_BUILD_SUFFIX must be 1..99; got '$TS_IOS_BUILD_SUFFIX'" >&2
    exit 2
  fi
  build=$build.$((10#$TS_IOS_BUILD_SUFFIX))
fi

echo "TS_VERSION=$version"
echo "TS_BUILD_NUMBER=$build"
