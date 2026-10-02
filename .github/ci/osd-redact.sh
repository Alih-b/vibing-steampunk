#!/usr/bin/env bash
# Filter (stdin to stdout) that strips local identifiers from what the OSD job
# publishes: the work dir becomes <work>, $HOME becomes <home>, and the host of
# $SAP_URL becomes <sap-host> (a loopback host is left as it is: it names
# nothing). Used on summary.json / summary.md and on the artifact copies of
# osd.log and build.json.
#
#   .github/ci/osd-redact.sh < in > out
#   OSD_WORKDIR=... SAP_URL=... .github/ci/osd-redact.sh < in > out
set -euo pipefail
host=""
if [ -n "${SAP_URL:-}" ]; then
  host=${SAP_URL#*://}; host=${host%%/*}; host=${host%:*}; host=${host#[}; host=${host%]}
  case "$host" in localhost|127.*|::1) host="" ;; esac
fi
work=${OSD_WORKDIR:-}
[ "${#work}" -gt 1 ] || work=""
home=${HOME:-}
[ "${#home}" -gt 1 ] || home=""
R_WORK=$work R_HOME=$home R_HOST=$host perl -pe '
  BEGIN { ($w, $h, $s) = @ENV{qw(R_WORK R_HOME R_HOST)} }
  s/\Q$w\E/<work>/g if length $w;
  s/\Q$h\E/<home>/g if length $h;
  s/\Q$s\E/<sap-host>/gi if length $s;
'
