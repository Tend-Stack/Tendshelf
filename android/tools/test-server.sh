#!/usr/bin/env bash
# Starts a throwaway Tendshelf server for the Android emulator checks.
#   android/tools/test-server.sh <port> <dir>
# Builds this worktree's server into <dir>, makes synthetic audio with ffmpeg, seeds owner "tester" and member
# "member" (password in <dir>/password), starts the server on 127.0.0.1:<port> and prints its pid (also in
# <dir>/server.pid). Stop it by that pid, then delete <dir>. Reach it from the emulator at http://10.0.2.2:<port>.
# Never use port 8095 or a real data folder. <dir> belongs under .local/tests/android/<pkg>/<run-id>/.
set -euo pipefail
port=${1:?port} dir=${2:?dir}
case "$port" in 8095) echo "refusing port 8095" >&2; exit 2;; esac
root=$(cd "$(dirname "$0")/../.." && pwd)
mkdir -p "$dir/books" "$dir/data"
dir=$(cd "$dir" && pwd)
pass=android-test-pass-1
echo "$pass" > "$dir/password"
(cd "$root" && CGO_ENABLED=0 go build -o "$dir/tendshelf" ./cmd/tendshelf && go build -o "$dir/androidseed" ./internal/testutil/androidseed)

tone() { # tone <seconds> <hz> <out> [extra ffmpeg args...]
  local s=$1 hz=$2 out=$3; shift 3
  ffmpeg -nostdin -loglevel error -y -f lavfi -i "sine=frequency=$hz:duration=$s" -ac 1 "$@" "$out"
}
b="$dir/books/Test Author"
# Parts: 3 MP3 files of 5 minutes. Chapter tags are written per file; the third chapter (at 270 s of file 1) runs
# on into file 2, which carries the same chapter name at 0 s, so a chapter crosses a file boundary.
mkdir -p "$b/Parts"
for i in 1 2 3; do
  case $i in
    1) meta=";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=150000\ntitle=One\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=150000\nEND=270000\ntitle=Two\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=270000\nEND=300000\ntitle=Three";;
    2) meta=";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=60000\ntitle=Three\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=60000\nEND=300000\ntitle=Four";;
    3) meta=";FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=150000\ntitle=Five\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=150000\nEND=300000\ntitle=Six";;
  esac
  printf "$meta\n" > "$dir/meta$i.txt"
  tone 300 $((300 + i * 80)) "$b/Parts/Part $i.mp3" -i "$dir/meta$i.txt" -map 0:a -map_metadata 1 -map_chapters 1 \
    -b:a 32k -metadata title="Parts" -metadata album="Parts" -metadata artist="Test Author" -metadata track="$i"
done
# Single: one M4B of 10 minutes, 4 chapters.
mkdir -p "$b/Single"
printf ';FFMETADATA1\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=150000\ntitle=A\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=150000\nEND=300000\ntitle=B\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=300000\nEND=450000\ntitle=C\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=450000\nEND=600000\ntitle=D\n' > "$dir/metaS.txt"
tone 600 440 "$b/Single/Single.m4b" -i "$dir/metaS.txt" -map 0:a -map_metadata 1 -map_chapters 1 -c:a aac -b:a 32k \
  -metadata title="Single" -metadata album="Single" -metadata artist="Test Author"
# Short 1 and Short 2: 30 s MP3, series "Shorts".
for n in 1 2; do
  mkdir -p "$b/Short $n"
  tone 30 $((500 + n * 100)) "$b/Short $n/Short $n.mp3" -b:a 32k -metadata title="Short $n" -metadata album="Short $n" \
    -metadata artist="Test Author" -metadata series="Shorts" -metadata series-part="$n" -metadata grouping="Shorts #$n"
done
# Odd: one Ogg Opus file, which the browser cannot always play, so needsCompat is true.
mkdir -p "$b/Odd"
tone 60 700 "$b/Odd/Odd.opus" -c:a libopus -b:a 24k -metadata title="Odd" -metadata album="Odd" -metadata artist="Test Author"
rm -f "$dir"/meta*.txt
# WITH_EBOOKS=1 (A9): synthetic EPUBs (one with a picture; TXT is not a supported ebook format), written by a Go test so the files are never real books.
if [ "${WITH_EBOOKS:-}" = 1 ]; then
  (cd "$root" && ANDROID_EBOOK_DIR="$dir/books" go test ./internal/testutil/androidseed -run TestWriteEbooks -count=1 > /dev/null)
fi

"$dir/androidseed" -data "$dir/data" -books "$dir/books" -password "$pass"
"$dir/tendshelf" -addr "127.0.0.1:$port" -data "$dir/data" > "$dir/server.log" 2>&1 &
echo $! > "$dir/server.pid"
for _ in $(seq 1 60); do
  if curl -fsS "http://127.0.0.1:$port/healthz" > /dev/null 2>&1; then echo "pid $(cat "$dir/server.pid")"; exit 0; fi
  sleep 0.5
done
echo "server did not start; see $dir/server.log" >&2
exit 1
