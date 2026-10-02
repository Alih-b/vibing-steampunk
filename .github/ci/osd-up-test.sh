#!/usr/bin/env bash
# Offline checks for osd-up.sh's checksum pin: a binary that does not match
# the committed sha256 is refused (exit 4) and never made executable.
#
#   .github/ci/osd-up-test.sh
#
# Uses a fake asset from a temp dir (OSD_DL_DIR) and a temp copy of
# .github/ci/osd.sha256 (OSD_PINS), and stops after the verdict
# (OSD_VERIFY_ONLY=1), so nothing is downloaded or run.
set -euo pipefail
here=$(cd "$(dirname "$0")" && pwd)
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
tag=$(tr -d '[:space:]' < "$here/osd.version")

case "$(uname -s)/$(uname -m)" in
  Linux/x86_64)  asset=osd-linux-x64 ;;
  Linux/aarch64) asset=osd-linux-arm64 ;;
  Darwin/arm64)  asset=osd-darwin-arm64 ;;
  *) echo "osd-up-test: no OSD asset for $(uname -s)/$(uname -m); nothing to test"; exit 0 ;;
esac

mkdir -p "$tmp/dl"
printf '#!/bin/sh\necho must never run >&2\nexit 99\n' > "$tmp/dl/$asset"
fake=$(sha256sum "$tmp/dl/$asset" | cut -d' ' -f1)
echo "$fake  $asset" > "$tmp/dl/$asset.sha256"
grep -q " $tag/$asset\$" "$here/osd.sha256" || { echo "osd-up-test: FAIL: no committed line for $tag/$asset"; exit 1; }

fails=0
# expect <want-exit> <name> <pins-file> [env...]
expect() {
  local want=$1 name=$2 pins=$3; shift 3
  local w="$tmp/w-$name" got=0
  env OSD_DL_DIR="$tmp/dl" OSD_PINS="$pins" OSD_VERIFY_ONLY=1 "$@" \
    "$here/osd-up.sh" "$w" > "$tmp/$name.out" 2> "$tmp/$name.err" || got=$?
  if [ "$got" != "$want" ]; then
    echo "osd-up-test: FAIL $name: exit $got, want $want"; sed 's/^/  /' "$tmp/$name.err"; fails=$((fails + 1))
  elif [ -e "$w/osd-vsp-ci" ]; then
    echo "osd-up-test: FAIL $name: the binary was installed"; fails=$((fails + 1))
  else
    echo "osd-up-test: ok   $name (exit $got): $(tail -1 "$tmp/$name.err")"
  fi
}

# Control: the committed file with this asset's line set to the fake's hash passes.
sed "s|^[0-9a-f]\{64\}  $tag/$asset\$|$fake  $tag/$asset|" "$here/osd.sha256" > "$tmp/pins-good"
expect 0 control "$tmp/pins-good"
grep -q '^OSD_PINNED=true$' "$tmp/control.out" || { echo "osd-up-test: FAIL control: not reported pinned"; fails=$((fails + 1)); }

# The committed hash, edited by one hex digit: refused although the release .sha256 agrees.
other=0
if [ "${fake:0:1}" = 0 ]; then other=1; fi
sed "s|^$fake  $tag/$asset\$|$other${fake:1}  $tag/$asset|" "$tmp/pins-good" > "$tmp/pins-edited"
cmp -s "$tmp/pins-good" "$tmp/pins-edited" && { echo "osd-up-test: FAIL: the edit did not change the pin"; exit 1; }
expect 4 edited-pin "$tmp/pins-edited"

# The real committed pin against the fake binary: refused.
expect 4 committed-pin "$here/osd.sha256"

# Asset and release .sha256 disagree: refused even with a matching pin.
cp -r "$tmp/dl" "$tmp/dl-good"
echo "0000000000000000000000000000000000000000000000000000000000000000  $asset" > "$tmp/dl/$asset.sha256"
expect 4 release-mismatch "$tmp/pins-good"
rm -rf "$tmp/dl"; mv "$tmp/dl-good" "$tmp/dl"

# A tag with no committed line: refused unless explicitly unpinned.
: > "$tmp/pins-empty"
expect 4 unpinned-refused "$tmp/pins-empty"
expect 0 unpinned-allowed "$tmp/pins-empty" OSD_ALLOW_UNPINNED=1
grep -q '^OSD_PINNED=false$' "$tmp/unpinned-allowed.out" || { echo "osd-up-test: FAIL unpinned-allowed: not reported unpinned"; fails=$((fails + 1)); }

if [ "$fails" != 0 ]; then
  echo "osd-up-test: $fails failed"
  exit 1
fi
echo "osd-up-test: all checks passed"
