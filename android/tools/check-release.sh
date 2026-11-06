#!/usr/bin/env bash
# Checks a release APK for debug-only code (plan A4). Usage: android/tools/check-release.sh <release.apk>
# The debug hooks live in src/debug; src/release holds a stub. This fails when any debug action, extra path or
# test address is in the APK's dex or manifest, or when the stub's methods do anything but return a constant.
set -euo pipefail
apk=${1:?release apk}
sdk=${ANDROID_HOME:?ANDROID_HOME}
bt=$(ls -d "$sdk"/build-tools/* | sort -V | tail -1)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
unzip -qo "$apk" 'classes*.dex' AndroidManifest.xml -d "$tmp"
fail=0
for f in "$tmp"/classes*.dex; do strings -a "$f" >> "$tmp/strings.txt"; "$bt/dexdump" -d "$f" >> "$tmp/dump.txt" 2>/dev/null || true; done
for bad in DEBUG_PLAYER DEBUG_EVAL DEBUG_LOAD DEBUG_TOKEN8 DEBUG_COOKIES_CLEAR DEBUG_READ keepAliveForDebug DEFAULT_SERVER 10.0.2.2 tokenRequestBody 'Lapp/tendshelf/android/DebugProbe;' 'Api;.signIn'; do
  if grep -qF -- "$bad" "$tmp/strings.txt" "$tmp/dump.txt"; then echo "FAIL: release dex holds: $bad"; fail=1; fi
done
"$bt/aapt2" dump xmltree --file AndroidManifest.xml "$apk" > "$tmp/manifest.txt"
for bad in DebugProbe DEBUG_ DebugHooks; do
  if grep -qF -- "$bad" "$tmp/manifest.txt"; then echo "FAIL: release manifest holds: $bad"; fail=1; fi
done
# The stub: each method of the release DebugHooks is a constant return, with no call inside.
body=$(awk "/Class descriptor  : 'Lapp\/tendshelf\/android\/DebugHooks;'/{on=1;next} /Class descriptor/{on=0} on" "$tmp/dump.txt")
if [ -z "$body" ]; then echo "FAIL: no DebugHooks stub found"; fail=1; fi
if echo "$body" | grep -E 'invoke-' | grep -vE 'Ljava/lang/Object;.<init>|DebugHooks;.<init>|Lkotlin/jvm/internal/Intrinsics;.checkNotNullParameter' | grep -q .; then echo "FAIL: the DebugHooks stub calls something"; fail=1; fi
[ $fail = 0 ] && echo "release apk clean: no debug action, probe, test address or sign-in path; DebugHooks is a stub"
exit $fail
