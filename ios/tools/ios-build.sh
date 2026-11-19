#!/usr/bin/env bash
# Builds and tests the iPhone app on a Mac (decision 0048). This is what CI will call; from Linux use
# ios/tools/mac-build.sh, which syncs the tree to the Mac and runs this there. See docs/ios.md.
#
#   ios-build.sh [shared|build|test|all]   (default: all)
#   ios-build.sh clean
#
# Environment:
#   TS_IOS_SIM        simulator name (default "iPhone 17")
#   TS_IOS_SIM_OS     iOS runtime of the simulator (default: the newest installed)
#   TS_BUILD_DESCRIBE version string baked into the build (default: git describe, else "unknown")
#
# Exit codes: 2 bad argument or wrong OS, 3 Xcode first-launch components missing, 4 no such simulator,
# 5 no Xcode project yet (before I1b), 6 reserved for mac-build.sh (the Mac is locked).
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

step=${1:-all}
case "$step" in
  shared | build | test | all | clean) ;;
  *)
    echo "usage: ios-build.sh [shared|build|test|all|clean]" >&2
    exit 2
    ;;
esac

if [ "$step" = clean ]; then
  rm -rf ios/build android/shared/build
  echo "clean: ok"
  exit 0
fi

if [ "$(uname -s)" != Darwin ]; then
  echo "ios-build.sh runs on a Mac; from Linux use ios/tools/mac-build.sh" >&2
  exit 2
fi

if ! xcodebuild -checkFirstLaunchStatus >/dev/null 2>&1; then
  echo "Xcode needs its first-launch components: run sudo xcodebuild -runFirstLaunch once" >&2
  exit 3
fi

JAVA_HOME=$(/usr/libexec/java_home -v 21)
export JAVA_HOME

sim=${TS_IOS_SIM:-iPhone 17}
os=${TS_IOS_SIM_OS:-}
if [ -z "$os" ]; then
  # The newest iOS runtime, e.g. "iOS 26.1" from "iOS 26.1 (26.1 - 23B80) - com.apple...".
  os=$(xcrun simctl list runtimes available | sed -nE 's/^iOS ([0-9.]+) .*/\1/p' | sort -t. -k1,1n -k2,2n -k3,3n | tail -n 1)
fi
if [ -z "$os" ]; then
  echo "no iOS simulator runtime is installed" >&2
  exit 4
fi
# The device lines under "-- iOS <os> --", exact name, then the UDID in parentheses.
udid=$(xcrun simctl list devices available | awk -v os="-- iOS $os --" -v name="$sim" '
  $0 == os { in_os = 1; next }
  /^-- / { in_os = 0 }
  in_os && index($0, "    " name " (") == 1 {
    match($0, /\([0-9A-F]{8}-[0-9A-F-]{27}\)/)
    print substr($0, RSTART + 1, RLENGTH - 2)
    exit
  }')
if [ -z "$udid" ]; then
  echo "no available simulator \"$sim\" on iOS $os" >&2
  exit 4
fi

describe=${TS_BUILD_DESCRIBE:-}
if [ -z "$describe" ]; then
  if [ -e .git ]; then
    describe=$(git describe --tags --long --dirty 2>/dev/null || echo unknown)
  else
    describe=unknown
  fi
fi

mkdir -p ios/build/logs ios/build/results
stamp=$(date -u +%Y%m%dT%H%M%SZ)

run_shared() {
  local log=ios/build/logs/shared.log
  if android/gradlew -p android --no-daemon -Ptendshelf.sharedOnly=true "-Ptendshelf.iosSimulator=$udid" \
    :shared:iosSimulatorArm64Test :shared:assembleTendshelfSharedDebugXCFramework >"$log" 2>&1; then
    echo "shared: ok"
  else
    echo "shared: FAILED (see $log)" >&2
    tail -n 20 "$log" >&2
    exit 1
  fi
}

# Runs xcodebuild build or test, prints one summary line, keeps the newest three result bundles.
run_xcode() {
  local action=$1
  local log=ios/build/logs/$action.log
  local bundle=ios/build/results/$action-$stamp.xcresult
  if [ ! -d ios/Tendshelf.xcodeproj ]; then
    echo "$action: no Xcode project yet (ios/Tendshelf.xcodeproj comes with I1b)" >&2
    exit 5
  fi
  local status=0
  xcodebuild -project ios/Tendshelf.xcodeproj -scheme Tendshelf -configuration Debug \
    -destination "id=$udid" -derivedDataPath ios/build/DerivedData -resultBundlePath "$bundle" \
    "TS_BUILD_DESCRIBE=$describe" "$action" >"$log" 2>&1 || status=$?
  if [ "$action" = test ] && [ -d "$bundle" ]; then
    local summary passed failed
    summary=$(xcrun xcresulttool get test-results summary --path "$bundle" 2>/dev/null || true)
    passed=$(printf '%s' "$summary" | plutil -extract passedTests raw - 2>/dev/null || echo "?")
    failed=$(printf '%s' "$summary" | plutil -extract failedTests raw - 2>/dev/null || echo "?")
    echo "test: $passed passed, $failed failed"
  elif [ "$status" -eq 0 ]; then
    echo "$action: ok"
  fi
  if [ "$status" -ne 0 ]; then
    echo "$action: FAILED (see $log)" >&2
    tail -n 20 "$log" >&2
    exit 1
  fi
  # Keep the newest three result bundles of all steps.
  # shellcheck disable=SC2012
  ls -1dt ios/build/results/*.xcresult 2>/dev/null | tail -n +4 | while read -r old; do rm -rf "$old"; done
}

case "$step" in
  shared) run_shared ;;
  build) run_xcode build ;;
  test) run_xcode test ;;
  all)
    run_shared
    run_xcode build
    run_xcode test
    ;;
esac
