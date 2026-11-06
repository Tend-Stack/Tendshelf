# Helpers for the emulator checks (plan A5b). Source it; it runs nothing. Use inside the emulator lock:
#   flock -w 3600 /home/rubirosa/Projects/Tendshelf/.local/android-emulator.lock bash -c '. android/tools/emu.sh; ...'
# Needs ANDROID_HOME. The package is the debug build's.
ADB=${ADB:-"$ANDROID_HOME/platform-tools/adb -s emulator-5554"}
PKG=app.tendshelf.android

sh_() { $ADB shell "$@"; }

# Activity on top, so a foreground start from the service is allowed (D2). Doze stays on.
front() { sh_ am start -n $PKG/.MainActivity > /dev/null; sleep 3; }

# Debug player ops (these keep the service foreground for a moment: not for reads).
op() { sh_ am startservice -n $PKG/.PlaybackService -a app.tendshelf.DEBUG_PLAYER --es op "$@" > /dev/null; }
startbook() { # startbook <book id> [atMs]
  if [ -n "${2:-}" ]; then op start --es book "$1" --el at "$2"; else op start --es book "$1"; fi
}

# A read by broadcast to the running process: no service start, no foreground (invariant 3). Prints the log line.
read_dump() {
  sh_ logcat -c
  sh_ am broadcast -a app.tendshelf.DEBUG_READ --es op "${1:-dump}" -p $PKG > /dev/null
  sleep 1
  sh_ logcat -d -s Tendshelf:I | grep -E 'dump |outbox |read:' | tail -1
}

# Is the service in the foreground? Prints the isForeground lines of the app's service record.
fgs() { sh_ dumpsys activity services $PKG | grep -E 'isForeground|foregroundId|ServiceRecord' | head -6; }

# The network policy line of the app's uid, e.g. procState=SVC ... blocked=DOZE.
uid_state() {
  local uid
  uid=$(sh_ pm list packages -U $PKG | sed -n 's/.*uid:\([0-9]*\).*/\1/p' | head -1)
  sh_ dumpsys netpolicy | grep -E "UID=$uid " | head -1
}

# A request from the shell passes even when the app is firewalled (UID 2000 is exempt): it proves the server, not the app.
shell_probe() { sh_ "curl -s -o /dev/null -w '%{http_code}' --max-time 5 http://10.0.2.2:${1:-8101}/healthz || toybox nc -w 3 10.0.2.2 ${1:-8101} </dev/null >/dev/null && echo reachable"; }
