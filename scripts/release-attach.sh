#!/bin/sh
# Attach files to the Gitea release of a tag, creating the release when it does not exist yet (release.yml).
# Safe to run again after a failed run: an existing release is reused, and a file already attached under the same
# name is replaced, so a half-uploaded file never stays. Each request retries, because the runners' Docker network
# can drop for a few seconds (curl exit 7, 2026-11-07 and 2026-11-08).
#
# Usage: scripts/release-attach.sh <tag> [--create] <file>...
# Needs API (the repository's API base URL) and RELEASE_TOKEN in the environment.
set -eu

tag="$1"
shift
create=no
if [ "${1:-}" = "--create" ]; then
	create=yes
	shift
fi
test -n "${RELEASE_TOKEN:-}" || { echo "secret RELEASE_TOKEN is not set"; exit 1; }
test -n "${API:-}" || { echo "API is not set"; exit 1; }

# call <expected status> <curl args>: retries network drops and 5xx answers (without -f, a 404 is an answer, not an
# error, so it is not retried), then fails unless the status is the expected one. The body goes where -o says.
call() {
	want="$1"
	shift
	got="$(curl -sS --retry 5 --retry-all-errors --retry-delay 5 -H "Authorization: token $RELEASE_TOKEN" -w '%{http_code}' "$@")"
	[ "$got" = "$want" ] || { echo "HTTP $got for: $*"; return 1; }
}

# The release's own id is the first "id" in its JSON; the author and the assets come after it.
code="$(curl -sS --retry 5 --retry-all-errors --retry-delay 5 -H "Authorization: token $RELEASE_TOKEN" -w '%{http_code}' \
	"$API/releases/tags/$tag" -o release.json)"
if [ "$code" = 404 ]; then
	[ "$create" = yes ] || { echo "no release for $tag"; exit 1; }
	call 201 -X POST "$API/releases" -H "Content-Type: application/json" \
		-d "{\"tag_name\":\"$tag\",\"name\":\"$tag\",\"body\":\"See CHANGELOG.md for what is in this release and how to upgrade.\"}" \
		-o release.json
elif [ "$code" != 200 ]; then
	echo "HTTP $code reading the release of $tag"
	exit 1
fi
id="$(grep -o '"id":[0-9]*' release.json | head -1 | cut -d: -f2)"
test -n "$id" || { echo "no release id in the answer"; head -c 300 release.json; exit 1; }

for f in "$@"; do
	name="$(basename "$f")"
	# Gitea writes an attachment as {"id":N,"name":"...",...}, so this finds an earlier upload of the same name.
	old="$(grep -o "\"id\":[0-9]*,\"name\":\"$name\"" release.json | head -1 | cut -d, -f1 | cut -d: -f2)"
	if [ -n "$old" ]; then
		call 204 -X DELETE "$API/releases/$id/assets/$old" -o /dev/null
		echo "replacing $name"
	fi
	call 201 -X POST "$API/releases/$id/assets?name=$name" -F "attachment=@$f" -o /dev/null
	echo "attached $name"
done
