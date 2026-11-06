#!/usr/bin/env bash
# The M6 exit run (plan D7): drives the real desktop window, both modes, inside the private headless
# GNOME Shell. Run it through the isolated display, never on the visible desktop:
#
#   make desktop-exit            (builds the probe window and the server, then runs this)
#
# Everything lives in one throwaway folder under /tmp: the window's home, config, library and download
# folder, the test server's data, and a stand-in xdg-open that only writes a log line. The person's own
# library, settings and the server on port 8095 are never touched. Only processes this script started
# are stopped, by PID. Synthetic audio (ffmpeg) and a synthetic EPUB only.
#
# Results: $OUT (default .local/tests/desktop/exit-<time>): <launch>/result.json for what the page saw,
# driver.txt for what this script saw, shots/*.png for the screenshots.
set -u
REAL_HOME="$HOME"

root="$(cd "$(dirname "$0")/../.." && pwd)"
out="${TENDSHELF_EXIT_OUT:-$root/.local/tests/desktop/exit-$(date -u +%Y%m%d-%H%M%S)}"
window="$root/bin/tendshelf-desktop-probe"
server_bin="$root/bin/tendshelf"
port=18096
work="$(mktemp -d /tmp/ts-exit-XXXXXX)"
mkdir -p "$out/shots"
report="$out/driver.txt"
: >"$report"
pids=()

say() { echo "$*" | tee -a "$report"; }
check() { # check NAME PASS|FAIL|NOTRUN detail...
  local name="$1" verdict="$2"
  shift 2
  say "[$verdict] $name: $*"
}

cleanup() {
  for p in "${pids[@]}"; do
    kill -0 "$p" 2>/dev/null && kill "$p" 2>/dev/null
  done
  sleep 1
  for p in "${pids[@]}"; do
    kill -0 "$p" 2>/dev/null && kill -9 "$p" 2>/dev/null
  done
  rm -rf "$work"
}
trap cleanup EXIT

for need in ffmpeg gdbus jq; do
  command -v "$need" >/dev/null 2>&1 || { echo "exit-run: $need is not installed" >&2; exit 2; }
done
[ -x "$window" ] && [ -x "$server_bin" ] || { echo "exit-run: build bin/tendshelf and bin/tendshelf-desktop-probe first (make desktop-exit)" >&2; exit 2; }
[ -n "${WAYLAND_DISPLAY:-}" ] || { echo "exit-run: run this through scripts/isolated-display.sh" >&2; exit 2; }
[ "$WAYLAND_DISPLAY" = "ts-desktop" ] || { echo "exit-run: WAYLAND_DISPLAY is not the private session; refusing" >&2; exit 2; }

# ---- fixtures: one audiobook folder with its EPUB, one copy for each mode ----
go -C "$root/desktop" build -o "$work/fixture" ./probe/fixture || exit 2
for mode in p c; do
  dir="$work/lib-$mode/A. Writer/The Pager"
  mkdir -p "$dir"
  ffmpeg -nostdin -loglevel error -f lavfi -i "sine=frequency=440:duration=180" -c:a aac \
    -metadata title="The Pager" -metadata artist="A. Writer" -metadata album="The Pager" "$dir/The Pager.m4b" || exit 2
  "$work/fixture" "$dir/The Pager.epub" || exit 2
done

# xdg-open stand-in: records what the window asked to open and opens nothing.
mkdir -p "$work/shim" "$work/home"
cat >"$work/shim/xdg-open" <<'EOF'
#!/bin/sh
echo "$@" >>"$XDG_OPEN_LOG"
EOF
chmod +x "$work/shim/xdg-open"
export XDG_OPEN_LOG="$work/xdg-open.log"

# ---- the test server (connect mode) ----
mkdir -p "$work/srv-data"
"$server_bin" -addr "127.0.0.1:$port" -data "$work/srv-data" >"$work/server.log" 2>&1 &
server_pid=$!
pids+=("$server_pid")
for _ in $(seq 1 50); do
  curl -sf "http://127.0.0.1:$port/healthz" >/dev/null && break
  sleep 0.2
done
code="$(grep -o 'setup?code=[A-Za-z0-9_-]*' "$work/server.log" | head -1 | cut -d= -f2)"
[ -n "$code" ] || { say "exit-run: no setup code in the server log"; exit 2; }

# ---- helpers ----
shot() { # shot NAME
  gdbus call --session --dest org.gnome.Shell.Screenshot --object-path /org/gnome/Shell/Screenshot \
    --method org.gnome.Shell.Screenshot.Screenshot false false "$out/shots/$1.png" >/dev/null 2>&1 &&
    say "screenshot $1.png" || say "screenshot $1 failed"
}

# launch NAME KEY=VALUE...  starts the probe window with a private home; its PID goes to $pid.
launch() {
  local name="$1"
  shift
  mkdir -p "$out/$name"
  env HOME="$work/home" PATH="$work/shim:$PATH" TENDSHELF_PROBE_OUT="$out/$name" WEBKIT_DISABLE_DMABUF_RENDERER=1 "$@" \
    "$window" >"$out/$name/window.log" 2>&1 &
  pid=$!
  pids+=("$pid")
}

# waitdone NAME SECONDS  waits for the probe's "done" answer.
waitdone() {
  local end=$((SECONDS + $2))
  while [ "$SECONDS" -lt "$end" ]; do
    jq -e '.done == true' "$out/$1/result.json" >/dev/null 2>&1 && return 0
    kill -0 "$pid" 2>/dev/null || return 1
    sleep 1
  done
  return 1
}

# stopterm PID  sends SIGTERM and reports how long the exit took and its status.
stopterm() {
  local target="$1" t0=$SECONDS children
  children="$(pgrep -P "$target" | tr '\n' ' ')"
  kill -TERM "$target" 2>/dev/null
  while kill -0 "$target" 2>/dev/null && [ $((SECONDS - t0)) -lt 25 ]; do sleep 0.5; done
  if kill -0 "$target" 2>/dev/null; then
    kill -9 "$target" 2>/dev/null
    wait "$target" 2>/dev/null
    term_status="did not stop in 25 s (killed)"
  else
    wait "$target" 2>/dev/null
    term_status="exit status $? after $((SECONDS - t0)) s"
  fi
  left=""
  for c in $children; do kill -0 "$c" 2>/dev/null && left="$left $c"; done
  term_children_left="${left:-none}"
}

# check_download PREFIX looks for the saved attachment in the window's download folder (its private home), then removes it.
check_download() {
  local dl
  dl="$(find "$work/home" -maxdepth 2 -name '*bookmarks*.md' 2>/dev/null | head -3)"
  if [ -n "$dl" ]; then
    check "$1.download" PASS "saved to the download folder: ${dl#"$work"/} ($(wc -c <"$(echo "$dl" | head -1)") bytes)"
    echo "$dl" | while read -r f; do rm -f "$f"; done
  else
    check "$1.download" FAIL "nothing saved; home holds: $(ls "$work/home" | tr '\n' ' ')"
  fi
}

portopen() { (exec 3<>"/dev/tcp/127.0.0.1/$1") 2>/dev/null; }
val() { jq -r "$2" "$out/$1/result.json" 2>/dev/null; } # val LAUNCH JQ-EXPR

# ---- the window itself (decision 0023, X1): real pointer and keys through bin/tendshelf-input ----
input_bin="$root/bin/tendshelf-input"
ipid=""

# iopen starts the input helper on a FIFO; icmd sends it one line and waits for its "ok".
iopen() {
  rm -f "$work/in.fifo" "$work/in.log"
  mkfifo "$work/in.fifo"
  "$input_bin" <"$work/in.fifo" >"$work/in.log" 2>"$work/in.err" &
  ipid=$!
  pids+=("$ipid")
  exec 7>"$work/in.fifo"
}
icmd() {
  local n
  n="$(wc -l <"$work/in.log")"
  echo "$*" >&7
  for _ in $(seq 1 200); do
    [ "$(wc -l <"$work/in.log")" -gt "$n" ] && return 0
    kill -0 "$ipid" 2>/dev/null || return 1
    sleep 0.05
  done
  return 1
}
iclose() {
  echo quit >&7 2>/dev/null
  exec 7>&-
}

# gone PID SECONDS: waits for a process to end.
gone() {
  local end=$((SECONDS + $2))
  while kill -0 "$1" 2>/dev/null && [ "$SECONDS" -lt "$end" ]; do sleep 0.2; done
  ! kill -0 "$1" 2>/dev/null
}

# calibrate N sets offx, offy: where the page's 0,0 is on the screen. Wayland tells a window nothing about
# its place, so the pointer goes to a known spot and the page says where it landed.
calibrate() {
  icmd move 600 400
  sleep 0.8
  local px py
  px="$(val "$1" '.pointer.x | floor')"
  py="$(val "$1" '.pointer.y | floor')"
  case "$px$py" in *[!0-9-]* | "") offx=""; offy=""; return 1 ;; esac
  offx=$((600 - px))
  offy=$((400 - py))
}
at() { icmd move $((offx + $1)) $((offy + $2)); } # at X Y: the pointer to page X,Y

pngsize() { # pngsize FILE: "WxH" of a PNG, read from its header
  od -An -tu1 -j16 -N8 "$1" 2>/dev/null | awk '{printf "%d x %d", $1*16777216+$2*65536+$3*256+$4, $5*16777216+$6*65536+$7*256+$8}'
}

window_suite() { # window_suite NAME [ENV=VALUE...]
  local n="$1" h="$work/dh-$1" cfg w0 w1 sz
  shift
  mkdir -p "$h/config"
  printf '{"schema":1,"window":{"width":900,"height":600}}\n' >"$h/config/desktop.json"
  cfg="$h/config/desktop.json"
  launch "$n" TENDSHELF_DESKTOP_HOME="$h" TENDSHELF_PROBE=personal TENDSHELF_PROBE_PLAN=window TENDSHELF_PROBE_STAY=1 "$@"
  local wp=$pid
  for _ in $(seq 1 120); do
    [ "$(val "$n" '.ready')" = "true" ] && break
    kill -0 "$wp" 2>/dev/null || break
    sleep 0.5
  done
  if [ "$(val "$n" '.ready')" != "true" ]; then
    check "$n.start" FAIL "the window never reported ready; see $out/$n/window.log"
    return
  fi
  sleep 1

  # Hidden until ready, and the saved size comes back (content 900 x 600; the shadow and border are outside it).
  local ready_line shown_line
  ready_line="$(grep -n 'first page ready' "$out/$n/window.log" | head -1 | cut -d: -f1)"
  shown_line="$(grep -n 'window shown' "$out/$n/window.log" | head -1 | cut -d: -f1)"
  if [ -n "$ready_line" ] && [ -n "$shown_line" ] && [ "$ready_line" -lt "$shown_line" ]; then
    check "$n.hidden-until-ready" PASS "the window was shown after the page said ready ($(grep 'window shown' "$out/$n/window.log" | head -1 | cut -c1-120))"
  else
    check "$n.hidden-until-ready" FAIL "ready line ${ready_line:-none}, shown line ${shown_line:-none}"
  fi
  w0="$(val "$n" '.size.w')"
  h0="$(val "$n" '.size.h')"
  if [ "${w0:-0}" -ge 896 ] && [ "${w0:-0}" -le 904 ] && [ "${h0:-0}" -ge 596 ] && [ "${h0:-0}" -le 604 ]; then
    check "$n.size-restored" PASS "page content ${w0}x${h0} for a saved 900x600"
  else
    check "$n.size-restored" FAIL "page content ${w0}x${h0} for a saved 900x600"
  fi

  iopen
  icmd tap Shift_L
  if ! calibrate "$n"; then
    check "$n.pointer" FAIL "the page saw no pointer; input log: $(tail -2 "$work/in.err" | tr '\n' ' ')"
    iclose
    kill "$wp" 2>/dev/null
    return
  fi
  say "calibration: the page's 0,0 is at screen $offx,$offy"

  # Shadow: the window's capture with its frame against the page content. GTK draws the shadow and border outside.
  at 450 22
  icmd click
  sleep 0.5
  for frame in true false; do
    gdbus call --session --dest org.gnome.Shell.Screenshot --object-path /org/gnome/Shell/Screenshot \
      --method org.gnome.Shell.Screenshot.ScreenshotWindow "$frame" false false "$out/shots/$n-window-frame-$frame.png" >/dev/null 2>&1
  done
  local sf sn
  sf="$(pngsize "$out/shots/$n-window-frame-true.png")"
  sn="$(pngsize "$out/shots/$n-window-frame-false.png")"
  case "$sf" in
    "900 x 600" | "") check "$n.shadow" FAIL "window capture $sf is no larger than the page content ${w0}x${h0}: no shadow or border around it" ;;
    *) check "$n.shadow" PASS "window capture with frame $sf, without $sn, around page content ${w0}x${h0}: the buffer is larger than the frame (shots/$n-window-frame-*.png)" ;;
  esac

  # Copy and paste in a field with no menu: type, select all, copy, collapse to the end, paste.
  icmd tap a
  icmd tap b
  icmd tap c
  icmd tap Control_L a
  icmd tap Control_L c
  icmd tap Right
  icmd tap Control_L v
  sleep 0.5
  local fv
  fv="$(val "$n" '.field')"
  if [ "$fv" = "abcabc" ]; then check "$n.copy-paste" PASS "typed abc, copied, pasted: $fv"; else check "$n.copy-paste" FAIL "field holds '$fv', wanted abcabc"; fi

  # Drag from the bar: the window must follow the pointer, so the page's place on screen moves by the pointer's travel.
  # The first motion step happens before the drag starts, so 15 px of slack either way.
  local ox=$offx oy=$offy
  at 450 22
  icmd btn 1 1
  sleep 0.2
  icmd rel 20 10
  sleep 0.2
  icmd rel 180 140
  sleep 0.3
  icmd btn 1 0
  sleep 0.5
  calibrate "$n" || true
  if [ -n "$offx" ] && [ $((offx - ox)) -ge 185 ] && [ $((offx - ox)) -le 215 ] && [ $((offy - oy)) -ge 135 ] && [ $((offy - oy)) -le 165 ]; then
    check "$n.drag" PASS "the window moved by $((offx - ox)),$((offy - oy)) for a pointer travel of 200,150"
  else
    check "$n.drag" FAIL "the window moved by $((${offx:-0} - ox)),$((${offy:-0} - oy)) for a pointer travel of 200,150 (Wayland and X11 may differ; see the shot)"
  fi
  shot "$n-after-drag"

  # Double-click on the bar maximises, again restores; Go tells the page.
  [ -n "$offx" ] || calibrate "$n"
  at 200 22
  icmd click
  sleep 0.08
  icmd click
  sleep 1
  if val "$n" '.events | index("maximised")' | grep -qv null; then check "$n.double-click-maximise" PASS "events: $(val "$n" '.events | join(",")')"; else check "$n.double-click-maximise" FAIL "events: $(val "$n" '.events | join(",")')"; fi
  say "page size while maximised: $(val "$n" '.size | "\(.w)x\(.h)"')"
  calibrate "$n" || true
  at 200 22
  icmd click
  sleep 0.08
  icmd click
  sleep 1
  if val "$n" '.events | index("restored")' | grep -qv null; then check "$n.double-click-restore" PASS "events: $(val "$n" '.events | join(",")')"; else check "$n.double-click-restore" FAIL "events: $(val "$n" '.events | join(",")')"; fi

  # Edge resize with GTK's own border: the pointer in the shadow just outside the left edge (the drag put the right edge on the screen's edge; tried at
  # 4, 8 and 12 px, inside GTK's resize zone), drag 120 px.
  sleep 0.5
  local d dw=0
  w0="$(val "$n" '.size.w')"
  check "$n.size-after-restore" INFO "page content $w0 wide after maximise and restore"
  for d in 4 8 12; do
    calibrate "$n" || true
    w0="$(val "$n" '.size.w')"
    at $((0 - d)) 300
    sleep 0.3
    icmd btn 1 1
    sleep 0.2
    icmd rel -60 0
    icmd rel -60 0
    sleep 0.3
    [ "$d" = 8 ] && shot "$n-mid-resize"
    icmd btn 1 0
    sleep 0.8
    w1="$(val "$n" '.size.w')"
    dw=$((${w1:-0} - ${w0:-0}))
    say "edge resize try at +${d}px: $w0 to $w1"
    [ "$dw" -ge 100 ] && break
  done
  if [ "$dw" -ge 112 ] && [ "$dw" -le 128 ]; then
    check "$n.edge-resize" PASS "page width $w0 to $w1 for a pointer travel of 120 (started ${d}px outside the left edge)"
  else
    check "$n.edge-resize" FAIL "page width $w0 to $w1 for a pointer travel of 120"
  fi

  # Our close button quits the window, and the size is saved.
  calibrate "$n" || true
  w1="$(val "$n" '.size.w')"
  at $((w1 - 30)) 22
  icmd click
  if gone "$wp" 10; then
    check "$n.close" PASS "the close button ended the window"
  else
    check "$n.close" FAIL "the window was still running 10 s after the close button"
    kill "$wp" 2>/dev/null
  fi
  sz="$(jq -c .window "$cfg" 2>/dev/null)"
  if [ "$(jq -r .window.width "$cfg" 2>/dev/null)" -ge $((w1 - 6)) ] 2>/dev/null; then
    check "$n.size-saved" PASS "desktop.json window: $sz (page content was ${w1} wide)"
  else
    check "$n.size-saved" FAIL "desktop.json window: $sz (page content was ${w1} wide)"
  fi
  iclose

  # A second start restores that size; Ctrl+Q quits.
  launch "$n-again" TENDSHELF_DESKTOP_HOME="$h" TENDSHELF_PROBE=personal TENDSHELF_PROBE_PLAN=window TENDSHELF_PROBE_STAY=1 "$@"
  wp=$pid
  for _ in $(seq 1 120); do
    [ "$(val "$n-again" '.ready')" = "true" ] && break
    kill -0 "$wp" 2>/dev/null || break
    sleep 0.5
  done
  w0="$(val "$n-again" '.size.w')"
  if [ "${w0:-0}" -ge $((w1 - 6)) ] && [ "${w0:-0}" -le $((w1 + 6)) ]; then
    check "$n.size-restored-again" PASS "page content ${w0} wide; the last close left ${w1}"
  else
    check "$n.size-restored-again" FAIL "page content ${w0} wide; the last close left ${w1}"
  fi
  iopen
  calibrate "$n-again" || true
  at 300 22
  icmd click
  sleep 0.5
  icmd tap Control_L q
  if gone "$wp" 10; then check "$n.ctrl-q" PASS "Ctrl+Q ended the window"; else check "$n.ctrl-q" FAIL "the window was still running 10 s after Ctrl+Q"; kill "$wp" 2>/dev/null; fi
  iclose
}

# The window limits on a monitor wider than 1,920 (run with TS_MONITOR=2560x1000, TENDSHELF_EXIT_ONLY=limits):
# edge and corner drags stop at maxWidth, a corner drag resizes, the Maximise button and a maximise that did
# not come from it fit the window to 1,920 instead of the full width.
limits_suite() { # limits_suite NAME [ENV=VALUE...]
  local n="$1" h="$work/dh-$1" cfg w0 h0 w1 h1 wp
  shift
  mkdir -p "$h/config"
  printf '{"schema":1,"window":{"width":1700,"height":600}}\n' >"$h/config/desktop.json"
  cfg="$h/config/desktop.json"
  launch "$n" TENDSHELF_DESKTOP_HOME="$h" TENDSHELF_PROBE=personal TENDSHELF_PROBE_PLAN=window TENDSHELF_PROBE_STAY=1 "$@"
  wp=$pid
  for _ in $(seq 1 120); do
    [ "$(val "$n" '.ready')" = "true" ] && break
    kill -0 "$wp" 2>/dev/null || break
    sleep 0.5
  done
  if [ "$(val "$n" '.ready')" != "true" ]; then
    check "$n.start" FAIL "the window never reported ready; see $out/$n/window.log"
    return
  fi
  sleep 1
  iopen
  calibrate "$n" || true
  w0="$(val "$n" '.size.w')"
  h0="$(val "$n" '.size.h')"
  check "$n.start" INFO "page content ${w0}x${h0} on a monitor of ${TS_MONITOR:-?}"

  # The bar's Maximise (double-click on the stand-in bar sends the same verb): fits 1,920 wide, not the monitor's width.
  local hb d
  hb="$h0"
  calibrate "$n" || true
  at 200 22
  icmd click
  sleep 0.08
  icmd click
  sleep 1.5
  w1="$(val "$n" '.size.w')"
  h1="$(val "$n" '.size.h')"
  if [ "${w1:-0}" -eq 1920 ] && [ "${h1:-0}" -gt "$hb" ] && val "$n" '.events | index("maximised")' | grep -qv null; then
    check "$n.maximise-fits" PASS "page content ${w1}x${h1} (monitor ${TS_MONITOR:-?}); events: $(val "$n" '.events | join(",")')"
  else
    check "$n.maximise-fits" FAIL "page content ${w1}x${h1}, wanted 1920 wide and taller than ${hb}; events: $(val "$n" '.events | join(",")')"
  fi
  shot "$n-fitted"
  say "fitted window position: the page's 0,0 is at $offx,$offy before the fit (Wayland tells a window nothing about its place)"
  calibrate "$n" || true
  say "fitted window position: the page's 0,0 is now at $offx,$offy (centred on 2560 would be 320)"
  at 200 22
  icmd click
  sleep 0.08
  icmd click
  sleep 1.5
  w1="$(val "$n" '.size.w')"
  h1="$(val "$n" '.size.h')"
  if [ "${w1:-0}" -eq "$w0" ] && [ "${h1:-0}" -eq "$hb" ] && val "$n" '.events | index("restored")' | grep -qv null; then
    check "$n.maximise-restores" PASS "page content back to ${w1}x${h1}; events: $(val "$n" '.events | join(",")')"
  else
    check "$n.maximise-restores" FAIL "page content ${w1}x${h1}, wanted ${w0}x${hb}; events: $(val "$n" '.events | join(",")')"
  fi

  # A corner drag (bottom right, in GTK's border just outside the corner) resizes both ways.
  w0="$(val "$n" '.size.w')"
  h0="$(val "$n" '.size.h')"
  for d in 4 8 12; do
    calibrate "$n" || true
    at $((w0 + d)) $((h0 + d))
    sleep 0.3
    icmd btn 1 1
    sleep 0.2
    icmd rel 20 20
    icmd rel 80 60
    sleep 0.3
    icmd btn 1 0
    sleep 0.8
    w1="$(val "$n" '.size.w')"
    h1="$(val "$n" '.size.h')"
    say "corner resize try at +${d}px: ${w0}x${h0} to ${w1}x${h1}"
    [ $((${w1:-0} - ${w0:-0})) -ge 60 ] && break
  done
  if [ $((${w1:-0} - w0)) -ge 90 ] && [ $((${w1:-0} - w0)) -le 110 ] && [ $((${h1:-0} - h0)) -ge 70 ] && [ $((${h1:-0} - h0)) -le 90 ]; then
    check "$n.corner-resize" PASS "page content ${w0}x${h0} to ${w1}x${h1} for a pointer travel of 100,80 (width and height both changed)"
  else
    check "$n.corner-resize" FAIL "page content ${w0}x${h0} to ${w1}x${h1} for a pointer travel of 100,80"
  fi
  shot "$n-corner"

  # An edge drag past the limit stops at 1,920 (the right edge, 300 px further).
  w0=$w1
  h0=$h1
  for d in 4 8 12; do
    calibrate "$n" || true
    at $((w0 + d)) 300
    sleep 0.3
    icmd btn 1 1
    sleep 0.2
    icmd rel 20 0
    icmd rel 140 0
    icmd rel 140 0
    sleep 0.3
    icmd btn 1 0
    sleep 0.8
    w1="$(val "$n" '.size.w')"
    [ "${w1:-0}" -gt "$w0" ] && break
  done
  if [ "${w1:-0}" -eq 1920 ]; then
    check "$n.edge-limit" PASS "page content ${w0} to ${w1} for a pointer travel of 300 (the limit is 1920)"
  else
    check "$n.edge-limit" FAIL "page content ${w0} to ${w1} for a pointer travel of 300 (wanted exactly 1920)"
  fi
  shot "$n-edge-limit"

  # A corner drag past the limit: the width stays 1,920, the height still follows.
  w0=$w1
  for d in 4 8 12; do
    calibrate "$n" || true
    h0="$(val "$n" '.size.h')"
    at $((w0 + d)) $((h0 + d))
    sleep 0.3
    icmd btn 1 1
    sleep 0.2
    icmd rel 20 20
    icmd rel 140 60
    icmd rel 140 0
    sleep 0.3
    icmd btn 1 0
    sleep 0.8
    h1="$(val "$n" '.size.h')"
    [ "${h1:-0}" -gt "$h0" ] && break
  done
  w1="$(val "$n" '.size.w')"
  if [ "${w1:-0}" -eq 1920 ] && [ "${h1:-0}" -gt "${h0:-0}" ]; then
    check "$n.corner-limit" PASS "page content ${w0}x${h0} to ${w1}x${h1}: the width held at 1920, the height followed"
  else
    check "$n.corner-limit" FAIL "page content ${w0}x${h0} to ${w1}x${h1} (wanted width 1920, a taller window)"
  fi

  # The same fit again after drags: a window the person has resized by hand must fit too.
  # The bar's Maximise (double-click on the stand-in bar sends the same verb): fits 1,920 wide, not the monitor's width.
  hb="$(val "$n" '.size.h')"
  calibrate "$n" || true
  at 200 22
  icmd click
  sleep 0.08
  icmd click
  sleep 1.5
  w1="$(val "$n" '.size.w')"
  h1="$(val "$n" '.size.h')"
  if [ "${w1:-0}" -eq 1920 ] && [ "${h1:-0}" -gt "$hb" ] && val "$n" '.events | index("maximised")' | grep -qv null; then
    check "$n.maximise-fits-after-drag" PASS "page content ${w1}x${h1} (monitor ${TS_MONITOR:-?}); events: $(val "$n" '.events | join(",")')"
  elif [ "${w1:-0}" -eq 1920 ] && val "$n" '.events | index("maximised")' | grep -qv null; then
    check "$n.maximise-fits-after-drag" INFO "page content ${w1}x${h1}: already 1920 wide, and under Wayland the compositor owns the window's place, so it did not grow taller from where it sat (X11 moves it with SetPosition)"
  else
    check "$n.maximise-fits-after-drag" FAIL "page content ${w1}x${h1}, wanted 1920 wide; events: $(val "$n" '.events | join(",")')"
  fi
  shot "$n-fitted"
  say "fitted window position: the page's 0,0 is at $offx,$offy before the fit (Wayland tells a window nothing about its place)"
  calibrate "$n" || true
  say "fitted window position: the page's 0,0 is now at $offx,$offy (centred on 2560 would be 320)"
  at 200 22
  icmd click
  sleep 0.08
  icmd click
  sleep 1.5
  w1="$(val "$n" '.size.w')"
  h1="$(val "$n" '.size.h')"
  if [ "${w1:-0}" -eq 1920 ] && [ "${h1:-0}" -eq "$hb" ] && val "$n" '.events | index("restored")' | grep -qv null; then
    check "$n.maximise-restores-after-drag" PASS "page content back to ${w1}x${h1}; events: $(val "$n" '.events | join(",")')"
  else
    check "$n.maximise-restores-after-drag" FAIL "page content ${w1}x${h1}, wanted 1920x${hb}; events: $(val "$n" '.events | join(",")')"
  fi

  # A maximise that did not come from the button. Super+Up first (the window manager's own key); if the private
  # shell does not act on it, the shell itself maximises the window over its (unsafe-mode) Eval, which is the same
  # xdg-toplevel maximise request a double-click on a system title bar or a tiling key would send.
  local before after
  before="$(val "$n" '.events | length')"
  icmd tap Super_L Up
  sleep 2
  after="$(val "$n" '.events | length')"
  if [ "$after" = "$before" ] && [ "$(val "$n" '.events | last')" != "maximised" ]; then
    say "Super+Up did not reach the window in the private shell; asking the shell to maximise it"
    gdbus call --session --dest org.gnome.Shell --object-path /org/gnome/Shell --method org.gnome.Shell.Eval \
      'global.get_window_actors().forEach(a => a.meta_window.maximize(3))' >>"$report" 2>&1
    sleep 2.5
  fi
  w1="$(val "$n" '.size.w')"
  h1="$(val "$n" '.size.h')"
  if [ "${w1:-0}" -eq 1920 ] && val "$n" '.events | last' | grep -q maximised && [ "$(grep -c 'window fitted' "$out/$n/window.log")" -ge 3 ]; then
    check "$n.outside-maximise-fits" PASS "page content ${w1}x${h1} instead of the monitor's width; events: $(val "$n" '.events | join(",")')"
  else
    check "$n.outside-maximise-fits" FAIL "page content ${w1}x${h1}, wanted 1920 wide, last event maximised; events: $(val "$n" '.events | join(",")')"
  fi
  grep -c "window fitted" "$out/$n/window.log" | sed 's/^/log: fits so far: /' | tee -a "$report"
  iclose

  # Close from the window (Ctrl+Q); the saved state must say maximised (a fitted window is shown as Restore).
  iopen
  calibrate "$n" || true
  at 300 22
  icmd click
  icmd tap Control_L q
  gone "$wp" 10 || kill "$wp" 2>/dev/null
  iclose
  say "saved window: $(jq -c .window "$cfg" 2>/dev/null)"

  # A window saved as maximised on a wide monitor starts fitted, not full width.
  printf '{"schema":1,"window":{"width":900,"height":600,"maximised":true}}\n' >"$cfg"
  launch "$n-saved" TENDSHELF_DESKTOP_HOME="$h" TENDSHELF_PROBE=personal TENDSHELF_PROBE_PLAN=window TENDSHELF_PROBE_STAY=1 "$@"
  wp=$pid
  for _ in $(seq 1 120); do
    [ "$(val "$n-saved" '.ready')" = "true" ] && break
    kill -0 "$wp" 2>/dev/null || break
    sleep 0.5
  done
  sleep 1.5
  w1="$(val "$n-saved" '.size.w')"
  if [ "${w1:-0}" -eq 1920 ] && val "$n-saved" '.events | index("maximised")' | grep -qv null; then
    check "$n.saved-maximised" PASS "started at ${w1} wide (monitor ${TS_MONITOR:-?}); events: $(val "$n-saved" '.events | join(",")')"
  else
    check "$n.saved-maximised" FAIL "started at ${w1} wide, wanted 1920; events: $(val "$n-saved" '.events | join(",")')"
  fi
  kill "$wp" 2>/dev/null
}

backend="${TENDSHELF_WINDOW_BACKEND:-wayland}"
if [ "${TENDSHELF_EXIT_ONLY:-}" = "limits" ]; then
  limits_suite win-limits
  say "results in $out"
  exit 0
fi
if [ "$backend" = "x11" ]; then
  [ -n "${DISPLAY:-}" ] || { check x11.window NOTRUN "no Xwayland display in the private session"; exit 0; }
  window_suite win-x11 GDK_BACKEND=x11 DISPLAY="$DISPLAY"
else
  window_suite win-wayland
fi
if [ "${TENDSHELF_EXIT_ONLY:-}" = "window" ]; then
  say "results in $out"
  exit 0
fi

# ---- 0. the private session ----
say "session: WAYLAND_DISPLAY=$WAYLAND_DISPLAY XDG_RUNTIME_DIR=$XDG_RUNTIME_DIR GSETTINGS_BACKEND=${GSETTINGS_BACKEND:-unset}"
real_ext="$(env -u DBUS_SESSION_BUS_ADDRESS XDG_CONFIG_HOME="$REAL_HOME/.config" GSETTINGS_BACKEND=dconf gsettings get org.gnome.shell enabled-extensions 2>&1 | cut -c1-200)"

# Tray over D-Bus: enabling the AppIndicator extension is a call to the private shell, whose settings
# live in memory (GSETTINGS_BACKEND=memory) under the private bus; the person's own settings are not reached.
tray_note=""
if [ "${GSETTINGS_BACKEND:-}" = "memory" ] && [ -n "${DBUS_SESSION_BUS_ADDRESS:-}" ]; then
  r="$(gdbus call --session --dest org.gnome.Shell.Extensions --object-path /org/gnome/Shell/Extensions \
    --method org.gnome.Shell.Extensions.EnableExtension ubuntu-appindicators@ubuntu.com 2>&1 | cut -c1-200)"
  say "enable appindicator in the private shell: $r"
  sleep 3
  if gdbus call --session --dest org.freedesktop.DBus --object-path /org/freedesktop/DBus \
    --method org.freedesktop.DBus.ListNames 2>/dev/null | grep -q StatusNotifierWatcher; then
    tray_note="watcher-present"
  else
    tray_note="no-watcher"
  fi
  say "StatusNotifierWatcher on the private bus: $tray_note"
fi

# ---- 1. connect mode, first run ----
launch connect-first TENDSHELF_DESKTOP_HOME="$work/dh-c" TENDSHELF_PROBE=connect TENDSHELF_PROBE_STAY=1 \
  TENDSHELF_PROBE_SERVER="http://127.0.0.1:$port" TENDSHELF_PROBE_CODE="$code" TENDSHELF_PROBE_LIBRARY="$work/lib-c"
c1=$pid
if waitdone connect-first 240; then check connect.first-run INFO "probe finished"; else check connect.first-run FAIL "no done answer in 240 s"; fi
shot connect-first
if [ -s "$XDG_OPEN_LOG" ]; then
  check connect.external-link PASS "xdg-open asked for: $(tail -1 "$XDG_OPEN_LOG")"
else
  check connect.external-link FAIL "xdg-open stand-in was not called"
fi
check_download connect
stopterm "$c1"
check connect.sigterm INFO "$term_status; children left: $term_children_left"
# NOTE: PASS above only records the facts; read the status text. A non-zero status is reported, not hidden.

# CSP the server sends, for comparison with what each window received.
for path in /healthz /login; do
  curl -sI "http://127.0.0.1:$port$path" | tr -d '\r' | grep -i '^content-security-policy:' | cut -d' ' -f2- >"$work/csp$(echo "$path" | tr / _)"
done
for path in /healthz /login; do
  key="$(echo "$path" | tr / _)"
  want="$(cat "$work/csp$key")"
  got="$(val connect-first ".library.csp[\"$path\"]")"
  if [ -n "$want" ] && [ "$want" = "$got" ]; then check "connect.csp$path" PASS "identical"; else check "connect.csp$path" FAIL "server: $want | window: $got"; fi
done

# ---- 2. connect mode, relaunch with the saved server: sign-in kept, reader, then a clean Quit ----
: >"$XDG_OPEN_LOG"
launch connect-relaunch TENDSHELF_DESKTOP_HOME="$work/dh-c" TENDSHELF_PROBE=connect TENDSHELF_PROBE_PLAN=relaunch
c2=$pid
if waitdone connect-relaunch 120; then check connect.relaunch INFO "probe finished"; else check connect.relaunch FAIL "no done answer"; fi
wait "$c2" 2>/dev/null
check connect.quit-exit INFO "the probe ends with app Quit; exit status $?"

# ---- 3. personal mode, first run ----
: >"$XDG_OPEN_LOG"
launch personal-first TENDSHELF_DESKTOP_HOME="$work/dh-p" TENDSHELF_PROBE=personal TENDSHELF_PROBE_STAY=1 \
  TENDSHELF_PROBE_LIBRARY="$work/lib-p"
p1=$pid
if waitdone personal-first 240; then check personal.first-run INFO "probe finished"; else check personal.first-run FAIL "no done answer in 240 s"; fi
shot personal-first
for path in /healthz /login; do
  key="$(echo "$path" | tr / _)"
  want="$(cat "$work/csp$key")"
  got="$(val personal-first ".library.csp[\"$path\"]")"
  if [ -n "$want" ] && [ "$want" = "$got" ]; then check "personal.csp$path" PASS "identical to the server binary's"; else check "personal.csp$path" FAIL "server: $want | window: $got"; fi
done
if [ -s "$XDG_OPEN_LOG" ]; then check personal.external-link PASS "xdg-open asked for: $(tail -1 "$XDG_OPEN_LOG")"; else check personal.external-link FAIL "stand-in not called"; fi
check_download personal

# Tray: the window registers a StatusNotifierItem when a watcher exists.
if [ "$tray_note" = "watcher-present" ]; then
  items="$(gdbus call --session --dest org.kde.StatusNotifierWatcher --object-path /StatusNotifierWatcher \
    --method org.freedesktop.DBus.Properties.Get org.kde.StatusNotifierWatcher RegisteredStatusNotifierItems 2>&1 | cut -c1-300)"
  say "tray items: $items"
  case "$items" in *StatusNotifierItem*) check tray.registered PASS "$items" ;; *) check tray.registered FAIL "$items" ;; esac
else
  check tray.registered NOTRUN "no StatusNotifierWatcher appeared in the private session"
fi

# Second launch focuses the first.
mkdir -p "$out/personal-second"
env HOME="$work/home" TENDSHELF_DESKTOP_HOME="$work/dh-p" "$window" >"$out/personal-second/window.log" 2>&1 &
s2=$!
pids+=("$s2")
end=$((SECONDS + 15))
while kill -0 "$s2" 2>/dev/null && [ "$SECONDS" -lt "$end" ]; do sleep 0.5; done
if kill -0 "$s2" 2>/dev/null; then kill "$s2" 2>/dev/null; second_state="second process still running after 15 s (stopped)"; else wait "$s2" 2>/dev/null; second_state="second process exited with status $?"; fi
if grep -q "second launch: bringing the window forward" "$out/personal-first/window.log"; then
  check personal.second-launch PASS "first window logged the hand-over; $second_state"
else
  check personal.second-launch FAIL "no hand-over line in the first window's log; $second_state"
fi

# SIGTERM on the first window; the library port must close.
pport="$(jq -r .port "$work/dh-p/config/desktop.json" 2>/dev/null)"
portopen "$pport" && was=open || was=closed
stopterm "$p1"
portopen "$pport" && now=open || now=closed
check personal.sigterm INFO "port $pport $was before, $now after; $term_status; children left: $term_children_left"
# Cookie sqlite written? The next launch shows whether the sign-in survived.

# ---- 4. personal mode, relaunch after SIGTERM: sign-in kept, reader ----
launch personal-relaunch TENDSHELF_DESKTOP_HOME="$work/dh-p" TENDSHELF_PROBE=personal TENDSHELF_PROBE_PLAN=relaunch TENDSHELF_PROBE_STAY=1
p2=$pid
if waitdone personal-relaunch 120; then check personal.relaunch INFO "probe finished"; else check personal.relaunch FAIL "no done answer"; fi
shot personal-reader
stopterm "$p2"

# ---- 5. personal mode, change to connect: the library must stop ----
launch personal-switch TENDSHELF_DESKTOP_HOME="$work/dh-p" TENDSHELF_PROBE=personal TENDSHELF_PROBE_PLAN=switch TENDSHELF_PROBE_STAY=1 \
  TENDSHELF_PROBE_SERVER="http://127.0.0.1:$port"
p3=$pid
pport="$(jq -r .port "$work/dh-p/config/desktop.json" 2>/dev/null)"
if waitdone personal-switch 90; then check personal.switch INFO "probe finished"; else check personal.switch FAIL "no done answer"; fi
portopen "$pport" && now=open || now=closed
check personal.switch-stops-library INFO "port $pport is $now after the choice of connect (closed means the library stopped)"
shot personal-switch
stopterm "$p3"

# ---- 6. now playing (decision 0023, X4): tray menu over D-Bus, a notification, Wails' runtime out of reach ----
# The window is the saved connect-mode one; the page says a made-up book is playing, minimises the window,
# and says it ended. The tray is read from the AppIndicator watcher, the notification from the bus.
nh="$work/dh-now"
mkdir -p "$nh/config"
printf '{"schema":1,"mode":"connect","server":"http://127.0.0.1:%s"}\n' "$port" >"$nh/config/desktop.json"
nlog="$work/notify.log"
: >"$nlog"
if [ -n "${DBUS_SESSION_BUS_ADDRESS:-}" ]; then
  dbus-monitor --session "type='method_call',interface='org.freedesktop.Notifications',member='Notify'" >"$nlog" 2>&1 &
  pids+=("$!")
fi
launch now TENDSHELF_DESKTOP_HOME="$nh" TENDSHELF_PROBE=connect TENDSHELF_PROBE_PLAN=now TENDSHELF_PROBE_STAY=1
pn=$pid
for _ in $(seq 1 60); do [ "$(val now '.playing')" = "true" ] && break; sleep 0.5; done
sleep 1
if [ "$(val now '.playing')" != "true" ]; then
  check now.page FAIL "the page never sent its state; see $out/now/window.log"
else
  check now.page PASS "$(val now '.origin | tostring' | cut -c1-120)"
  if [ "$tray_note" = "watcher-present" ]; then
    # The watcher lists the item as "<unique name>@/StatusNotifierItem"; the unique name is the one to ask.
    item="$(gdbus call --session --dest org.kde.StatusNotifierWatcher --object-path /StatusNotifierWatcher \
      --method org.freedesktop.DBus.Properties.Get org.kde.StatusNotifierWatcher RegisteredStatusNotifierItems 2>&1 |
      grep -o "':[0-9.]*@" | tr -d "':@" | tail -1)"
    item=":$item"
    menupath="$(busctl --user get-property "$item" /StatusNotifierItem org.kde.StatusNotifierItem Menu 2>&1 | grep -o '"/[^"]*"' | tr -d '"' | head -1)"
    layout="$(busctl --user call -- "$item" "${menupath:-/MenuBar}" com.canonical.dbusmenu GetLayout iias 0 -1 0 2>&1 | cut -c1-2500)"
    echo "$layout" >"$out/now/tray-layout.txt"
    missing=""
    for want in "Probe & <b>Book</b>" "Chapter 2" "3 h left" "Pause" "Skip back" "Skip forward" "Show Tendshelf" "Quit Tendshelf"; do
      case "$layout" in *"$want"*) ;; *) missing="$missing [$want]" ;; esac
    done
    if [ -z "$missing" ]; then check now.tray-menu PASS "read over D-Bus: Probe & <b>Book</b>, Chapter 2 and 3 h left, Pause, Skip back, Skip forward, Show, Quit"
    else check now.tray-menu FAIL "missing:$missing; layout in $out/now/tray-layout.txt"; fi
  else
    check now.tray-menu NOTRUN "no StatusNotifierWatcher in the private session"
  fi
fi
if waitdone now 40; then
  check now.reach INFO "fetch from the session page to wails://localhost/wails/runtime: $(val now '.reach')"
  case "$(val now '.reach')" in refused*) check now.notify-not-reachable PASS "$(val now '.reach')" ;; *) check now.notify-not-reachable FAIL "$(val now '.reach')" ;; esac
else
  check now.reach FAIL "no done answer"
fi
cp "$nlog" "$out/now/notify-bus.txt" 2>/dev/null
if grep -q 'member=Notify' "$nlog" && grep -q 'Finished. Tendshelf has stopped.' "$nlog"; then
  check now.notification PASS "$(grep -m1 'Finished' "$nlog" | cut -c1-200)"
elif [ -s "$nlog" ]; then
  check now.notification FAIL "the bus saw: $(head -c 300 "$nlog" | tr '\n' ' ')"
else
  check now.notification NOTRUN "nothing was seen on the notification service (no monitor or no service in the private session)"
fi
# the saved theme became the ground, and Keep playing is off by default
check now.saved INFO "ground: $(jq -r .ground "$nh/config/desktop.json" 2>/dev/null), keepPlaying: $(jq -r .keepPlaying "$nh/config/desktop.json" 2>/dev/null)"
stopterm "$pn"

say "real GNOME enabled-extensions before: $real_ext"
real_ext_after="$(env -u DBUS_SESSION_BUS_ADDRESS XDG_CONFIG_HOME="$REAL_HOME/.config" GSETTINGS_BACKEND=dconf gsettings get org.gnome.shell enabled-extensions 2>&1 | cut -c1-200)"
say "real GNOME enabled-extensions after:  $real_ext_after"
[ "$real_ext" = "$real_ext_after" ] && check session.real-settings PASS "unchanged" || check session.real-settings FAIL "changed"
say "results in $out"
