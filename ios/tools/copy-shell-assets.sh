#!/usr/bin/env bash
# Run Script phase "Copy shell assets" (decision 0048): the bundled pages and the server's own stylesheets and fonts
# go into the app as ShellAssets/, the iPhone twin of the Android CopyShellAssets task. One copy of the pages lives in
# android/app/src/main/assets/; nothing is duplicated in ios/.
set -euo pipefail

root=$(cd "$SRCROOT/.." && pwd)
out="$TARGET_BUILD_DIR/$UNLOCALIZED_RESOURCES_FOLDER_PATH/ShellAssets"
static="$root/internal/web/static"

rm -rf "$out"
mkdir -p "$out/fonts"
cp "$root"/android/app/src/main/assets/* "$out/"
cp "$static/css/tokens.css" "$static/css/bundle.css" "$out/"
# fonts.css names /static/fonts/...; the bundled origin serves them from /assets/fonts/.
sed 's#/static/fonts/#/assets/fonts/#g' "$static/css/fonts.css" >"$out/fonts.css"
cp "$static"/fonts/*.woff2 "$out/fonts/"
cp "$root/android/app/src/main/res/values/strings.xml" "$out/strings.xml"
