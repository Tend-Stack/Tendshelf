#!/bin/sh
# Builds bin/tendshelf-desktop_<describe>_<arch>.deb from bin/tendshelf-desktop with dpkg-deb.
# <describe> is `git describe --tags --long --dirty`; the control file's Version is the plain
# YEAR.RELEASE.FIX of the last tag (no leading v, no commit suffix).
#   build-deb.sh                      build the package (run `make desktop` first; `make deb` does both)
#   build-deb.sh --version <describe> print the plain version for a describe string, then stop
set -eu

plain_version() {
	# v2026.1.0-366-g650d5a1-dirty -> 2026.1.0
	printf '%s\n' "$1" | sed -E 's/^v//; s/-[0-9]+-g[0-9a-f]+(-dirty)?$//; s/-dirty$//'
}

if [ "${1:-}" = "--version" ]; then
	plain_version "$2"
	exit 0
fi

here="$(cd "$(dirname "$0")" && pwd)"
root="$(cd "$here/../.." && pwd)"
describe="$(git -C "$root" describe --tags --long --dirty)"
version="$(plain_version "$describe")"
arch="$(dpkg --print-architecture)"
binary="$root/bin/tendshelf-desktop"
icon="$root/internal/web/static/img/icon-512.png"
out="$root/bin/tendshelf-desktop_${describe}_${arch}.deb"
[ -x "$binary" ] || { echo "missing $binary; run make desktop" >&2; exit 1; }

stage="$(mktemp -d)"
trap 'rm -rf "$stage"' EXIT
chmod 0755 "$stage"
install -D -m 0755 "$binary" "$stage/usr/bin/tendshelf-desktop"
install -D -m 0644 "$here/tendshelf-desktop.desktop" "$stage/usr/share/applications/org.tendshelf.Tendshelf.desktop"
install -D -m 0644 "$icon" "$stage/usr/share/icons/hicolor/512x512/apps/org.tendshelf.Tendshelf.png"
mkdir -p "$stage/DEBIAN"
size="$(du -sk "$stage" | cut -f1)"
sed -e "s/@VERSION@/$version/" -e "s/@ARCH@/$arch/" -e "s/@SIZE@/$size/" "$here/control.in" > "$stage/DEBIAN/control"
# No maintainer scripts: the package only places files, at install time and at run time.
dpkg-deb --root-owner-group --build "$stage" "$out" >/dev/null
echo "$out"
