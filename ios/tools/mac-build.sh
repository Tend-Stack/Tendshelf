#!/usr/bin/env bash
# Runs ios/tools/ios-build.sh on the Mac from Linux (decision 0048; docs/ios.md): syncs this tree to the Mac,
# runs the step there under a lock, prints its output. Nothing is installed on the Mac.
#
#   TS_MAC_HOST=<ssh host> ios/tools/mac-build.sh [shared|build|test|all|clean]
#
# Environment:
#   TS_MAC_HOST  the Mac's ssh host (required)
#   TS_MAC_DIR   directory on the Mac, relative to its home (default "tendshelf-ios"); the tree lands in
#                $TS_MAC_DIR/<name of this checkout>
#
# Exit codes: 2 TS_MAC_HOST unset, 6 another build holds the Mac; the rest come from ios-build.sh.
set -euo pipefail

repo=$(cd "$(dirname "$0")/../.." && pwd)
cd "$repo"

if [ -z "${TS_MAC_HOST:-}" ]; then
  echo "set TS_MAC_HOST to the Mac's ssh host" >&2
  exit 2
fi

step=${1:-all}
base=${TS_MAC_DIR:-tendshelf-ios}
dir=$base/$(basename "$repo")
ssh_opts=(-o BatchMode=yes)

ssh "${ssh_opts[@]}" "$TS_MAC_HOST" "mkdir -p '$base'"

if [ "$step" = clean ]; then
  ssh "${ssh_opts[@]}" "$TS_MAC_HOST" "rm -rf '$dir' '$dir.lock'"
  echo "clean: removed $TS_MAC_HOST:$dir"
  exit 0
fi

# One build at a time on the Mac: mkdir is atomic. The trap removes the lock when this script ends, however it ends.
if ! ssh "${ssh_opts[@]}" "$TS_MAC_HOST" "mkdir '$dir.lock'" 2>/dev/null; then
  echo "another build holds the Mac: $dir.lock" >&2
  exit 6
fi
trap 'ssh "${ssh_opts[@]}" "$TS_MAC_HOST" "rmdir '"'$dir.lock'"'" >/dev/null 2>&1 || true' EXIT

describe=$(git describe --tags --long --dirty 2>/dev/null || echo unknown)

ssh "${ssh_opts[@]}" "$TS_MAC_HOST" "mkdir -p '$dir'"
rsync -a --delete --exclude-from=ios/tools/rsync-exclude -e "ssh ${ssh_opts[*]}" "$repo/" "$TS_MAC_HOST:$dir/"

ssh "${ssh_opts[@]}" "$TS_MAC_HOST" "cd '$dir' && TS_BUILD_DESCRIBE='$describe' ios/tools/ios-build.sh $step"
