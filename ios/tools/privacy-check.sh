#!/usr/bin/env bash
# Checks that the privacy manifest declares every required-reason API a built app uses (decision 0048, I4d).
# Mac only (nm, strings, plutil). Apple rejects an upload that uses such an API without a declared reason.
#
#   privacy-check.sh <Tendshelf.app>
#
# For the app binary and each app extension: the undefined symbols (nm -u) and the strings are matched to the
# four categories the app may use. A used category its manifest does not declare is an error (exit 1); a declared
# category that is not seen is only a warning line, because a reason can be given for a call made through a
# string-built selector.
#
# Exit codes: 1 a used category is not declared, 2 bad argument.
set -euo pipefail

app=${1:-}
if [ -z "$app" ] || [ ! -d "$app" ]; then
  echo "usage: privacy-check.sh <Tendshelf.app>" >&2
  exit 2
fi
if [ "$(uname -s)" != Darwin ]; then
  echo "privacy-check.sh runs on a Mac" >&2
  exit 2
fi

# The names that show a category is in use (Apple's "Describing use of required reason API" lists).
tokens() {
  case "$1" in
    FileTimestamp) echo "_stat _fstat _lstat _fstatat _getattrlist _getattrlistbulk _fgetattrlist _getattrlistat modificationDate creationDate fileModificationDate NSFileModificationDate NSFileCreationDate NSURLContentModificationDateKey NSURLCreationDateKey _NSURLContentModificationDateKey _NSURLCreationDateKey attributesOfItemAtPath:error:" ;;
    SystemBootTime) echo "_mach_absolute_time systemUptime" ;;
    DiskSpace) echo "_statfs _statvfs _fstatfs _fstatvfs systemFreeSize NSURLVolumeAvailableCapacityKey _NSURLVolumeAvailableCapacityKey _NSURLVolumeAvailableCapacityForImportantUsageKey _NSURLVolumeAvailableCapacityForOpportunisticUsageKey _NSURLVolumeTotalCapacityKey volumeAvailableCapacityForImportantUsage volumeAvailableCapacityForOpportunisticUsage" ;;
    UserDefaults) echo "_OBJC_CLASS_\$_NSUserDefaults NSUserDefaults standardUserDefaults" ;;
  esac
}
categories="FileTimestamp SystemBootTime DiskSpace UserDefaults"

failed=0

# check <bundle directory> <label>: the bundle's executable against the bundle's own manifest.
check() {
  local dir=$1 label=$2 exe bin declared="" cat t found used
  exe=$(plutil -extract CFBundleExecutable raw -o - "$dir/Info.plist")
  bin=$dir/$exe
  if [ -f "$dir/PrivacyInfo.xcprivacy" ]; then
    declared=$(plutil -extract NSPrivacyAccessedAPITypes json -o - "$dir/PrivacyInfo.xcprivacy" |
      { grep -o 'NSPrivacyAccessedAPICategory[A-Za-z]*' || true; } | sed 's/NSPrivacyAccessedAPICategory//' | sort -u | tr '\n' ' ') || true
  else
    echo "$label: no PrivacyInfo.xcprivacy in the bundle" >&2
    failed=1
  fi
  local names
  names=$(mktemp)
  { nm -u "$bin" 2>/dev/null || true; strings -a "$bin"; } >"$names"
  for cat in $categories; do
    found=""
    for t in $(tokens "$cat"); do
      if grep -qxF -- "$t" "$names"; then
        found=$t
        break
      fi
    done
    used=no
    [ -n "$found" ] && used=yes
    case " $declared " in
      *" $cat "*)
        if [ "$used" = yes ]; then
          echo "$label: $cat used ($found), declared"
        else
          echo "$label: warning: $cat declared but not seen"
        fi
        ;;
      *)
        if [ "$used" = yes ]; then
          echo "$label: ERROR: $cat used ($found) but not declared in its PrivacyInfo.xcprivacy" >&2
          failed=1
        fi
        ;;
    esac
  done
  rm -f "$names"
}

check "$app" "app"
for appex in "$app"/PlugIns/*.appex; do
  [ -d "$appex" ] || continue
  check "$appex" "$(basename "$appex")"
done

if [ "$failed" -ne 0 ]; then
  echo "privacy-check: FAILED" >&2
  exit 1
fi
echo "privacy-check: ok"
