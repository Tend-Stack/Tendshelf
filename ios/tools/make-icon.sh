#!/usr/bin/env bash
# Renders the iPhone app icon from the design handoff's icon (the one Android draws) into the asset catalog.
# Linux, needs a Chromium-family browser: TENDSHELF_TEST_BROWSER names it (default "chromium").
# The result is committed, so this runs only when the handoff's icon changes. See docs/ios.md.
set -euo pipefail
root=$(cd "$(dirname "$0")/../.." && pwd)
cd "$root"

svg=$root/docs/design/tendshelf-handoff/design-system/assets/logos/tendshelf-app-icon.svg
out=ios/Tendshelf/Assets.xcassets/AppIcon.appiconset/AppIcon-1024.png
browser=${TENDSHELF_TEST_BROWSER:-chromium}

# The background colour is the icon's own first rect, so the page behind the rounded corners matches it.
fill=$(sed -nE 's/.*<rect [^>]*fill="(#[0-9A-Fa-f]{6})".*/\1/p' "$svg" | head -n 1)
if [ -z "$fill" ]; then
  echo "no background colour found in $svg" >&2
  exit 1
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
sed -e "s|__FILL__|$fill|" -e "s|__SVG__|file://$svg|" ios/tools/icon.html >"$tmp/icon.html"
"$browser" --headless --no-sandbox --hide-scrollbars --force-device-scale-factor=1 --window-size=1024,1024 \
  --allow-file-access-from-files "--screenshot=$tmp/icon.png" "file://$tmp/icon.html" >/dev/null 2>&1
go run ios/tools/iconflat.go "$tmp/icon.png" "$out"
echo "icon: $out"
