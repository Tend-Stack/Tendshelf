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

say "real GNOME enabled-extensions before: $real_ext"
real_ext_after="$(env -u DBUS_SESSION_BUS_ADDRESS XDG_CONFIG_HOME="$REAL_HOME/.config" GSETTINGS_BACKEND=dconf gsettings get org.gnome.shell enabled-extensions 2>&1 | cut -c1-200)"
say "real GNOME enabled-extensions after:  $real_ext_after"
[ "$real_ext" = "$real_ext_after" ] && check session.real-settings PASS "unchanged" || check session.real-settings FAIL "changed"
say "results in $out"
