#!/usr/bin/env bash
# Turn `go test -tags=integration -json` output into the OSD capability matrix.
#
#   .github/ci/osd-summary.sh test.json summary.json summary.md
#
# Only top-level tests defined in integration-tagged files are counted; the
# ordinary unit tests the tag also builds are left out. Every failure and skip
# keeps its own first message, and is put in one class so that one
# missing route does not flood triage:
#
#   vacuous-pass      passed although a route it called is not served: the test
#                     tolerates the error, so the pass proves nothing here
#   missing-endpoint  OSD answered "<path> is not served by OSD" (a 404 route gap)
#   missing-object    "<TYPE> <NAME> does not exist": the test reads an SAP-standard
#                     object (SAPMSSY0, I_ABAPPACKAGE, /DMO/...) the target does not ship
#   timeout           the 30 s test client gave up (OSD activation rebuilds the runtime)
#   environment       the runner lacks something (a browser, an RFC channel, a fixture variable),
#                     or the browser-auth tests cannot reach an SSO page (GitHub runners ship Chrome)
#   different-answer  anything else: the target answered, and not the way the test expects
#   skipped           the test skipped itself, with its own reason
#
# Nothing is hidden: the counts are of what ran, and the reason is the test's.
#
# Meant for OSD runs only. Pointed at a run against a real SAP system it would
# still work, but the reasons quote that system's messages; osd-redact.sh
# strips $HOME, the work dir and a non-loopback $SAP_URL host, not object
# names or message text. Do not publish a summary of a run against A4H.
#
# OSD_PINNED=false (osd-up.sh, the nightly "latest" row) marks the summary
# "unpinned".
set -euo pipefail
in=${1:?test.json}; json=${2:?summary.json}; md=${3:?summary.md}
here=$(cd "$(dirname "$0")" && pwd)
root=$(git rev-parse --show-toplevel)
module=$(cd "$root" && go list -m)

tests=$(cd "$root" && grep -rl --include='*_test.go' '^//go:build integration' . |
  while read -r f; do
    pkg="$module/$(dirname "${f#./}")"
    grep -oE '^func Test[A-Za-z0-9_]+' "$f" | sed "s|^func |$pkg |"
  done | jq -R 'split(" ") | {key: (.[0] + " " + .[1]), value: true}' | jq -s from_entries)

jq -s --argjson tests "$tests" '
  # The reason is the most specific line: the SAP/OSD <message> when there is
  # one, else the last test line that reads like a failure, else the first.
  def reason: (map(select(test("<message[^>]*>"))) | map(capture("<message[^>]*>(?<m>[^<]*)").m) | last) as $m
    | (map(select(test("_test\\.go:[0-9]+:"))) | map(gsub("^\\s+"; ""))) as $t
    | (($t | map(select(test("(?i)fail|error|refused|not |cannot|did not"))) | last) // ($t | first) // "") as $line
    | ($line | gsub("<\\?xml.*$"; "") | gsub("\\s+"; " ") | .[0:220]) + (if $m then " -- " + $m else "" end);
  def class($action; $text):
    if $action == "pass" and ($text | test("is not served by OSD")) then "vacuous-pass"
    elif $action == "pass" then "pass"
    elif $action == "skip" then "skipped"
    elif ($text | test("is not served by OSD")) then "missing-endpoint"
    elif ($text | test("browser_auth_integration_test\\.go:[0-9]+: navigation failed")) then "environment"
    elif ($text | test("context deadline exceeded|Client.Timeout")) then "timeout"
    elif ($text | test("executable file not found|no RFC channel|only one transport ran|required for")) then "environment"
    elif ($text | test("<message[^>]*>[A-Z/]+ [A-Z0-9_/$]+ does not exist")) then "missing-object"
    else "different-answer" end;
  [ .[] | select(.Test != null) ] as $ev
  | [ $ev[] | select(.Test | contains("/") | not)
      | select(.Action == "pass" or .Action == "fail" or .Action == "skip")
      | select($tests[.Package + " " + .Test]) ] as $done
  | [ $done[] | . as $d
      | ([ $ev[] | select(.Package == $d.Package and (.Test == $d.Test or (.Test | startswith($d.Test + "/"))) and .Action == "output") | .Output ]
         | join("") | split("\n")) as $lines
      | ($lines | join(" ")) as $all
      | { package: ($d.Package | sub("^.*/pkg/"; "pkg/")), test: $d.Test, result: $d.Action,
          class: class($d.Action; $all), seconds: $d.Elapsed,
          reason: (if class($d.Action; $all) == "pass" then "" else ($lines | reason) end) } ]
  | sort_by(.package, .test)
  | { schema: "vsp-osd-matrix/1", target: env.OSD_TAG, pinned: (env.OSD_PINNED != "false"),
      counts: (group_by(.class) | map({key: .[0].class, value: length}) | from_entries),
      total: length, tests: . }' "$in" | "$here/osd-redact.sh" > "$json"

{
  echo "## vsp integration tests against OSD ${OSD_TAG:-}$([ "${OSD_PINNED:-}" = false ] && echo ' (unpinned)')"
  echo
  jq -r '"\(.total) tests: " + (.counts | to_entries | map("\(.value) \(.key)") | join(", "))' "$json"
  echo
  echo "| package | test | result | class | reason |"
  echo "|---|---|---|---|---|"
  jq -r '.tests[] | "| \(.package) | \(.test) | \(.result) | \(.class) | \(.reason | gsub("\\|"; "\\\\|")) |"' "$json"
} > "$md"
jq -r '"osd-integration\(if .pinned then "" else " (unpinned)" end): \(.total) tests: " + (.counts | to_entries | map("\(.value) \(.key)") | join(", "))' "$json"
