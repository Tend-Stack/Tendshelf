#!/usr/bin/env bash
# The pages reader in the real desktop window (WebKitGTK), inside the private headless GNOME Shell:
#
#   TS_MONITOR=1440x900 scripts/isolated-display.sh desktop/probe/pages-run.sh
#
# Needs bin/tendshelf and bin/tendshelf-desktop-probe (make build desktop-probe). Everything lives in one
# throwaway folder under /tmp; the person's library, settings and port 8095 are never touched. Synthetic
# audio and EPUB only. Results: $TENDSHELF_PAGES_OUT (default .local/tests/desktop/pages-<time>): result.json
# per launch, shots/*.png. Only processes this script started are stopped, by PID.
set -u
root="$(cd "$(dirname "$0")/../.." && pwd)"
out="${TENDSHELF_PAGES_OUT:-$root/.local/tests/desktop/pages-$(date -u +%Y%m%d-%H%M%S)}"
window="$root/bin/tendshelf-desktop-probe"
server_bin="$root/bin/tendshelf"
port="${TENDSHELF_PAGES_PORT:-18097}"
work="$(mktemp -d /tmp/ts-pages-XXXXXX)"
mkdir -p "$out/shots"
pids=()
cleanup() {
  for p in "${pids[@]}"; do kill -0 "$p" 2>/dev/null && kill "$p" 2>/dev/null; done
  sleep 1
  for p in "${pids[@]}"; do kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null; done
  rm -rf "$work"
}
trap cleanup EXIT
[ "${WAYLAND_DISPLAY:-}" = "ts-desktop" ] || { echo "pages-run: run this through scripts/isolated-display.sh" >&2; exit 2; }

go -C "$root/desktop" build -o "$work/fixture" ./probe/fixture || exit 2
dir="$work/lib/A. Writer/The Pager"
mkdir -p "$dir" "$work/home" "$work/srv-data"
ffmpeg -nostdin -loglevel error -f lavfi -i "sine=frequency=440:duration=180" -c:a aac \
  -metadata title="The Pager" -metadata artist="A. Writer" -metadata album="The Pager" "$dir/The Pager.m4b" || exit 2
"$work/fixture" "$dir/The Pager.epub" || exit 2

"$server_bin" -addr "127.0.0.1:$port" -data "$work/srv-data" >"$work/server.log" 2>&1 &
pids+=("$!")
for _ in $(seq 1 50); do curl -sf "http://127.0.0.1:$port/healthz" >/dev/null && break; sleep 0.2; done
code="$(grep -o 'setup?code=[A-Za-z0-9_-]*' "$work/server.log" | head -1 | cut -d= -f2)"
[ -n "$code" ] || { echo "no setup code"; exit 2; }

shot() {
  gdbus call --session --dest org.gnome.Shell.Screenshot --object-path /org/gnome/Shell/Screenshot \
    --method org.gnome.Shell.Screenshot.Screenshot false false "$out/shots/$1.png" >/dev/null 2>&1 && echo "screenshot $1.png" || echo "screenshot $1 failed"
}
launch() {
  local name="$1"
  shift
  mkdir -p "$out/$name"
  env HOME="$work/home" TENDSHELF_PROBE_OUT="$out/$name" ${TENDSHELF_PAGES_ENV:-WEBKIT_PROBE=1} ${TENDSHELF_PAGES_EXTRA:-} "$@" "$window" >"$out/$name/window.log" 2>&1 &
  pid=$!
  pids+=("$pid")
}
has() { jq -e "has(\"$2\")" "$out/$1/result.json" >/dev/null 2>&1; } # has LAUNCH KEY
waitkey() { # waitkey LAUNCH KEY SECONDS
  local end=$((SECONDS + $3))
  while [ "$SECONDS" -lt "$end" ]; do
    has "$1" "$2" && return 0
    kill -0 "$pid" 2>/dev/null || return 1
    sleep 0.2
  done
  return 1
}

launch first TENDSHELF_DESKTOP_HOME="$work/dh" TENDSHELF_PROBE=connect TENDSHELF_PROBE_STAY=1 \
  TENDSHELF_PROBE_SERVER="http://127.0.0.1:$port" TENDSHELF_PROBE_CODE="$code" TENDSHELF_PROBE_LIBRARY="$work/lib"
waitkey first done 240 && echo "first run done" || echo "first run FAILED"
kill "$pid"
sleep 2

launch pages ${TENDSHELF_PAGES_ONLY:+TENDSHELF_PAGES_ONLY=$TENDSHELF_PAGES_ONLY} TENDSHELF_DESKTOP_HOME="$work/dh" TENDSHELF_PROBE=connect TENDSHELF_PROBE_PLAN=pages TENDSHELF_PROBE_STAY=1
p="$pid"
if [ "${TENDSHELF_PAGES_ONLY:-}" = exp ]; then
  # Style variants tried on a held flight (see expScript in pages.go); one screenshot each.
  for v in $(echo "$TENDSHELF_PAGES_VARIANTS" | jq -r 'keys_unsorted[]'); do
    waitkey pages "exp.$v" 90 && shot "exp-$v" || echo "no $v"
  done
  waitkey pages done 30
  jq . "$out/pages/result.json" >"$out/pages-result.txt" 2>/dev/null
  exit 0
fi
if [ "${TENDSHELF_PAGES_ONLY:-}" = nav ]; then waitkey pages done 60; echo "nav run over"; else waitkey pages layout 90 && sleep 0.5 && shot flip-before; fi
for m in mid.1@25 mid.1@50 mid.1@75 mid.2@25 mid.2@50 mid.2@75; do
  waitkey pages "$m" 60 && shot "flip-$m" || echo "no $m"
done
for m in slow.1 slow.2; do
  if waitkey pages "$m" 60; then
    for i in 1 2 3 4 5 6 7; do shot "flip-$m-$i"; sleep 0.1; done
  fi
done
waitkey pages done 120 && echo "pages done" || echo "pages NOT done"
shot after-leave
jq . "$out/pages/result.json" >"$out/pages-result.txt" 2>/dev/null
echo "results in $out"
kill "$p"
sleep 1
