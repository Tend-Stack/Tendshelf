#!/usr/bin/env bash
# Archives the iPhone app for the App Store on the Mac (decision 0048, I4d). From Linux: make ios-archive.
#
#   archive.sh [--unsigned]
#
# --unsigned builds the Release archive without signing, to check the release build; it makes nothing to upload.
# Without it the archive is signed with the owner's own settings and exported as an .ipa. Signing lives on the Mac
# only: ~/.config/tendshelf/Signing.local.xcconfig (or the file named by TS_SIGNING_XCCONFIG), copied to
# ios/Config/Signing.local.xcconfig for the one archive and removed after. See docs/ios.md, App Store checklist.
#
# Environment: TS_VERSION and TS_BUILD_NUMBER (from ios/tools/version.sh; default: version.sh when there is a .git
# directory, else 0.0.0 and 1), TS_BUILD_DESCRIBE, TS_SIGNING_XCCONFIG.
#
# Exit codes: 2 bad argument or wrong OS, 3 Xcode first-launch components missing, 7 no signing set up or a signing
# setting is empty, 8 the signing identity is not in the keychain, 9 a provisioning profile is not installed,
# 10 a store build needs a version, 1 the archive or export failed.
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

unsigned=0
case "${1:-}" in
  "") ;;
  --unsigned) unsigned=1 ;;
  *)
    echo "usage: archive.sh [--unsigned]" >&2
    exit 2
    ;;
esac

if [ "$(uname -s)" != Darwin ]; then
  echo "archive.sh runs on a Mac; from Linux use make ios-archive" >&2
  exit 2
fi
if ! xcodebuild -checkFirstLaunchStatus >/dev/null 2>&1; then
  echo "Xcode needs its first-launch components: run sudo xcodebuild -runFirstLaunch once" >&2
  exit 3
fi

signing_in_tree=ios/Config/Signing.local.xcconfig
signing_source=${TS_SIGNING_XCCONFIG:-$HOME/.config/tendshelf/Signing.local.xcconfig}
copied=0
cleanup() {
  if [ "$copied" -eq 1 ]; then
    rm -f "$signing_in_tree"
  fi
}
trap cleanup EXIT

if [ "$unsigned" -eq 0 ]; then
  if [ ! -e "$signing_in_tree" ] && [ -e "$signing_source" ]; then
    cp "$signing_source" "$signing_in_tree"
    copied=1
  fi
  if [ ! -e "$signing_in_tree" ]; then
    echo "No signing set up on this Mac. Put your Signing.local.xcconfig at ~/.config/tendshelf/ (docs/ios.md, App Store checklist), or run with --unsigned to check the release build." >&2
    exit 7
  fi
fi

# Versions: from the environment, else version.sh when this is a Git checkout, else a development build.
version=${TS_VERSION:-}
build=${TS_BUILD_NUMBER:-}
if [ -z "$version" ]; then
  if [ -e .git ]; then
    eval "$(ios/tools/version.sh | sed 's/^/export /')"
    version=$TS_VERSION
    build=$TS_BUILD_NUMBER
  else
    version=0.0.0
    build=1
  fi
fi
build=${build:-1}

if [ "$unsigned" -eq 0 ] && [ "$version" = 0.0.0 ]; then
  echo "A store build needs a version: tag the commit vYEAR.RELEASE.FIX or set TS_VERSION." >&2
  exit 10
fi

describe=${TS_BUILD_DESCRIBE:-}
if [ -z "$describe" ]; then
  if [ -e .git ]; then
    describe=$(git describe --tags --long --dirty 2>/dev/null || echo unknown)
  else
    describe=unknown
  fi
fi

project=ios/Tendshelf.xcodeproj
dist=ios/build/dist
name=Tendshelf-$version-$build
archive=$dist/$name.xcarchive
mkdir -p ios/build/logs "$dist"

# A build setting of the Release configuration, as the project and the xcconfig files resolve it.
setting() {
  xcodebuild -project "$project" -target Tendshelf -configuration Release -showBuildSettings 2>/dev/null |
    sed -nE "s/^ *$1 = (.*)$/\\1/p" | head -n 1
}

team="" identity="" profile_app="" profile_widget=""
if [ "$unsigned" -eq 0 ]; then
  team=$(setting DEVELOPMENT_TEAM)
  identity=$(setting CODE_SIGN_IDENTITY)
  profile_app=$(setting TS_PROFILE_APP)
  profile_widget=$(setting TS_PROFILE_WIDGET)
  for pair in "DEVELOPMENT_TEAM:$team" "CODE_SIGN_IDENTITY:$identity" "TS_PROFILE_APP:$profile_app" "TS_PROFILE_WIDGET:$profile_widget"; do
    if [ -z "${pair#*:}" ] || [ "${pair#*:}" = "-" ]; then
      echo "Signing.local.xcconfig sets no ${pair%%:*} (docs/ios.md, App Store checklist)." >&2
      exit 7
    fi
  done
  if ! security find-identity -v -p codesigning | grep -qF -- "$identity"; then
    echo "The signing identity \"$identity\" is not in the keychain (docs/ios.md, App Store checklist)." >&2
    exit 8
  fi
  for profile in "$profile_app" "$profile_widget"; do
    found=0
    for folder in "$HOME/Library/Developer/Xcode/UserData/Provisioning Profiles" "$HOME/Library/MobileDevice/Provisioning Profiles"; do
      [ -d "$folder" ] || continue
      for file in "$folder"/*.mobileprovision; do
        [ -f "$file" ] || continue
        pname=$(security cms -D -i "$file" 2>/dev/null | plutil -extract Name raw -o - - 2>/dev/null || true)
        if [ "$pname" = "$profile" ]; then
          found=1
          break 2
        fi
      done
    done
    if [ "$found" -eq 0 ]; then
      echo "The provisioning profile \"$profile\" is not installed on this Mac (docs/ios.md, App Store checklist)." >&2
      exit 9
    fi
  done
fi

JAVA_HOME=$(/usr/libexec/java_home -v 21)
export JAVA_HOME
TS_IOS_CONFIG=Release ios/tools/ios-build.sh shared

log=ios/build/logs/archive.log
args=(-project "$project" -scheme Tendshelf -configuration Release -destination generic/platform=iOS
  -archivePath "$archive" -derivedDataPath ios/build/DerivedData-archive
  "TS_BUILD_DESCRIBE=$describe" "MARKETING_VERSION=$version" "CURRENT_PROJECT_VERSION=$build")
if [ "$unsigned" -eq 1 ]; then
  args+=(CODE_SIGNING_ALLOWED=NO)
fi
rm -rf "$archive"
status=0
xcodebuild archive "${args[@]}" >"$log" 2>&1 || status=$?
if [ "$status" -ne 0 ]; then
  if grep -q errSecInternalComponent "$log"; then
    echo "Unlock the login keychain first: security unlock-keychain ~/Library/Keychains/login.keychain-db" >&2
  fi
  echo "archive: FAILED (see $log)" >&2
  tail -n 20 "$log" >&2
  exit 1
fi

app=$archive/Products/Applications/Tendshelf.app
result=$archive
if [ "$unsigned" -eq 0 ]; then
  options=ios/build/ExportOptions.plist
  rm -f "$options"
  plutil -create xml1 "$options"
  plutil -insert method -string app-store-connect "$options"
  plutil -insert destination -string export "$options"
  plutil -insert teamID -string "$team" "$options"
  plutil -insert signingStyle -string manual "$options"
  plutil -insert provisioningProfiles -dictionary "$options"
  plutil -insert provisioningProfiles.app\\.tendshelf\\.ios -string "$profile_app" "$options"
  plutil -insert provisioningProfiles.app\\.tendshelf\\.ios\\.widget -string "$profile_widget" "$options"
  plutil -insert uploadSymbols -bool true "$options"
  plutil -insert manageAppVersionAndBuildNumber -bool false "$options"
  export_dir=$dist/$name
  rm -rf "$export_dir"
  status=0
  xcodebuild -exportArchive -archivePath "$archive" -exportOptionsPlist "$options" -exportPath "$export_dir" \
    >ios/build/logs/export.log 2>&1 || status=$?
  if [ "$status" -ne 0 ]; then
    if grep -q errSecInternalComponent ios/build/logs/export.log; then
      echo "Unlock the login keychain first: security unlock-keychain ~/Library/Keychains/login.keychain-db" >&2
    fi
    echo "archive: export FAILED (see ios/build/logs/export.log)" >&2
    tail -n 20 ios/build/logs/export.log >&2
    exit 1
  fi
  result=$(ls "$export_dir"/*.ipa)
fi

ios/tools/privacy-check.sh "$app"

# Keep the newest three archives (and their exports).
# shellcheck disable=SC2012
ls -1dt "$dist"/*.xcarchive 2>/dev/null | tail -n +4 | while read -r old; do
  rm -rf "$old" "${old%.xcarchive}"
done

if [ "$unsigned" -eq 1 ]; then
  echo "archive: ok (unsigned, version $version build $build) $result"
else
  echo "archive: ok (version $version build $build) $result"
fi
