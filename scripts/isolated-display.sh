#!/usr/bin/env bash
# Runs a command inside a private headless GNOME Shell (decision 0020, point 10).
#
# Everything is private: its own D-Bus session, Wayland socket, runtime, config, data and cache
# folders, all under one temporary directory that is removed at the end. Nothing opens on the
# visible desktop and the person's own session and settings are not touched. No install, no sudo.
#
# Usage: scripts/isolated-display.sh COMMAND [ARGS...]
# The exit status is the command's. The shell's log is removed on success and kept (its path is
# printed) on failure; TS_ISOLATED_LOG names it.
set -u

if [ "$#" -eq 0 ]; then
  echo "usage: $0 COMMAND [ARGS...]" >&2
  exit 2
fi
for need in gnome-shell dbus-run-session; do
  command -v "$need" >/dev/null 2>&1 || { echo "isolated-display: $need is not installed" >&2; exit 2; }
done

# The socket path must stay under 108 bytes, so the folder name is short.
base="$(mktemp -d /tmp/ts-XXXXXX)"
log="${TS_ISOLATED_LOG:-$base.shell.log}"
display="ts-desktop"
session_pid=""

# Stops only the process this script started, by its PID (never by a pattern).
cleanup() {
  if [ -n "$session_pid" ] && kill -0 "$session_pid" 2>/dev/null; then
    kill "$session_pid" 2>/dev/null
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$session_pid" 2>/dev/null || break
      sleep 0.5
    done
    kill -9 "$session_pid" 2>/dev/null
  fi
  rm -rf "$base"
}
trap cleanup EXIT

mkdir -p "$base/run" "$base/config" "$base/data" "$base/cache"
chmod 700 "$base/run"

# The inner script runs inside dbus-run-session: it starts the shell, waits for the socket, runs the
# command, and stops the shell by its PID. Its exit status is the command's.
inner='
set -u
gnome-shell --headless --wayland --no-x11 --wayland-display="$TS_DISPLAY" --virtual-monitor 1280x800 --unsafe-mode >>"$TS_LOG" 2>&1 &
shell=$!
ok=0
for _ in $(seq 1 100); do
  if [ -S "$XDG_RUNTIME_DIR/$TS_DISPLAY" ]; then ok=1; break; fi
  kill -0 "$shell" 2>/dev/null || break
  sleep 0.2
done
if [ "$ok" != 1 ]; then
  echo "isolated-display: the private GNOME Shell did not start; see $TS_LOG" >&2
  kill "$shell" 2>/dev/null
  exit 3
fi
sleep 1
WAYLAND_DISPLAY="$TS_DISPLAY" GDK_BACKEND=wayland "$@"
status=$?
kill "$shell" 2>/dev/null
wait "$shell" 2>/dev/null
exit "$status"
'

env -u DISPLAY -u WAYLAND_DISPLAY \
  XDG_RUNTIME_DIR="$base/run" \
  XDG_CONFIG_HOME="$base/config" \
  XDG_DATA_HOME="$base/data" \
  XDG_CACHE_HOME="$base/cache" \
  GSETTINGS_BACKEND=memory \
  TS_LOG="$log" \
  TS_DISPLAY="$display" \
  dbus-run-session -- bash -c "$inner" isolated "$@" &
session_pid=$!
wait "$session_pid"
status=$?
session_pid=""
if [ "$status" -ne 0 ] && [ -f "$log" ]; then
  echo "isolated-display: exit $status; shell log at $log" >&2
else
  rm -f "$log"
fi
exit "$status"
