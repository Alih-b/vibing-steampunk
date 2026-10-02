#!/usr/bin/env bash
# lint.sh: golangci-lint for CI, failing closed.
#
#   ./.github/ci/lint.sh gate [base]   # blocking: new issues since the merge base
#                                      # with <base> (default origin/main)
#   ./.github/ci/lint.sh full [base]   # advisory: the whole tree, a debt count;
#                                      # with a base, new code over a complexity
#                                      # threshold also becomes a warning annotation
#
# "Fail closed" means a run that did not demonstrably lint is red, never
# "0 issues". In gate mode:
#   - Any golangci-lint exit other than 0 or 1 (config error, package-load
#     failure, timeout, crash) is red.
#   - Its JSON report must parse, and must not carry a report-level error.
#   - A canary package (.github/ci/lint-canary.go.txt) is written into the tree
#     for the run. It holds one known finding per gate linter. Every one of the
#     five must come back: that proves each linter ran, loaded code, and that
#     new-code filtering still lets new code through. Missing canary findings
#     are red.
#   - Any finding outside the canary is a new issue, and red.
# Under GitHub Actions each new issue is also an inline annotation, and the
# summary line is a `ci-report` annotation for the PR report.
#
# Needs golangci-lint on PATH. If GOLANGCI_LINT_VERSION is set (CI pins it), the
# binary must be that version.
set -uo pipefail

mode=${1:-gate}
base=${2:-origin/main}
repo=$(git rev-parse --show-toplevel) || exit 2
cd "$repo" || exit 2

gate_linters=(errcheck govet ineffassign staticcheck unused)
canary_dir=internal/lintcanary
out=$(mktemp -d)

in_ci() { [ "${GITHUB_ACTIONS:-}" = "true" ]; }
report() { echo "$1"; if in_ci; then echo "::notice title=ci-report::$1"; fi; }
die() {
  echo "lint: $1" >&2
  if in_ci; then echo "::error title=lint::$1"; echo "::notice title=ci-report::$1"; fi
  exit 1
}
wrote_canary=
cleanup() { rm -rf "$out"; if [ -n "$wrote_canary" ]; then rm -rf "${canary_dir:?}"; fi; }
trap cleanup EXIT

# setup-go registers a problem matcher that turns every `file:line:col: msg`
# log line into an annotation. In full mode that would pin ten random old
# findings on the PR; the gate writes its own annotations for new issues.
if in_ci; then echo "::remove-matcher owner=go::"; fi

command -v golangci-lint >/dev/null || die "golangci-lint is not on PATH"
command -v jq >/dev/null || die "jq is not on PATH"
have=$(golangci-lint version --short 2>/dev/null || golangci-lint version 2>&1)
echo "golangci-lint $have"
if [ -n "${GOLANGCI_LINT_VERSION:-}" ] && [ "${have#v}" != "${GOLANGCI_LINT_VERSION#v}" ]; then
  die "golangci-lint is $have, but CI pins ${GOLANGCI_LINT_VERSION}"
fi

case "$mode" in
gate)
  [ -e "$canary_dir" ] && die "$canary_dir already exists; it is reserved for the lint canary"
  git rev-parse --verify --quiet "$base^{commit}" >/dev/null || die "base $base is not a commit here (fetch it first)"
  mb=$(git merge-base "$base" HEAD) || die "no merge base between $base and HEAD (shallow clone?)"
  # On a pull request the merge base must be behind HEAD. If it is HEAD itself
  # (a wrong base, or a checkout of the base instead of the PR), "new since the
  # merge base" is empty and the gate would pass having checked nothing.
  if [ "${GITHUB_EVENT_NAME:-}" = "pull_request" ] && [ "$mb" = "$(git rev-parse HEAD)" ]; then
    die "merge base with $base is HEAD itself: nothing would count as new, so the gate would check nothing"
  fi
  wrote_canary=1
  mkdir -p "$canary_dir" && cp .github/ci/lint-canary.go.txt "$canary_dir/canary.go" || die "cannot write the canary"

  status=0
  golangci-lint run --new-from-merge-base="$base" \
    --output.text.path=stderr --output.json.path="$out/lint.json" --show-stats=false ./... 2>"$out/lint.txt" || status=$?
  case $status in
    0) die "golangci-lint reported nothing, not even the canary: the gate did not lint" ;;
    1) ;;
    *) cat "$out/lint.txt" >&2; die "golangci-lint failed (exit $status): config, load or timeout error, not a lint verdict" ;;
  esac
  jq -e '(.Issues | type) == "array"' "$out/lint.json" >/dev/null 2>&1 || die "golangci-lint wrote no readable JSON report"
  err=$(jq -r '.Report.Error // empty' "$out/lint.json")
  [ -z "$err" ] || die "golangci-lint report error: $err"

  missing=()
  for l in "${gate_linters[@]}"; do
    jq -e --arg l "$l" --arg d "$canary_dir/" \
      'any(.Issues[]; .FromLinter == $l and (.Pos.Filename | startswith($d)))' "$out/lint.json" >/dev/null ||
      missing+=("$l")
  done
  [ ${#missing[@]} -eq 0 ] || die "canary not caught by: ${missing[*]} (that linter did not run, or new code was filtered out)"

  jq -r --arg d "$canary_dir/" '.Issues[] | select(.Pos.Filename | startswith($d) | not)
    | "\(.Pos.Filename):\(.Pos.Line):\(.Pos.Column): \(.Text) (\(.FromLinter))"' "$out/lint.json" > "$out/new.txt"
  n=$(wc -l < "$out/new.txt")
  if in_ci; then
    jq -r --arg d "$canary_dir/" '.Issues[] | select(.Pos.Filename | startswith($d) | not)
      | "::error file=\(.Pos.Filename),line=\(.Pos.Line),col=\(.Pos.Column),title=\(.FromLinter)::\(.Text | gsub("\n"; " "))"' "$out/lint.json"
  fi
  if [ "$n" -gt 0 ]; then
    cat "$out/new.txt"
    by=$(jq -r --arg d "$canary_dir/" '[.Issues[] | select(.Pos.Filename | startswith($d) | not) | .FromLinter]
      | group_by(.) | map("\(.[0]) \(length)") | join(", ")' "$out/lint.json")
    report "${n} new issues since ${base} (${by}) · canary ${#gate_linters[@]}/${#gate_linters[@]} · golangci-lint ${have}"
    exit 1
  fi
  report "0 new issues since ${base} · canary ${#gate_linters[@]}/${#gate_linters[@]} · golangci-lint ${have}"
  ;;

full)
  status=0
  golangci-lint run -c .github/ci/golangci-full.yml \
    --output.text.path=stdout --output.json.path="$out/lint.json" --show-stats=false ./... || status=$?
  case $status in
    0 | 1) ;;
    *) die "golangci-lint failed (exit $status): config, load or timeout error, not a lint verdict" ;;
  esac
  # A clean run writes "Issues": null.
  jq -e '(.Issues // [] | type) == "array"' "$out/lint.json" >/dev/null 2>&1 || die "golangci-lint wrote no readable JSON report"
  n=$(jq '.Issues // [] | length' "$out/lint.json")
  files=$(jq '[(.Issues // [])[].Pos.Filename] | unique | length' "$out/lint.json")
  by=$(jq -r '[(.Issues // [])[].FromLinter] | group_by(.) | map({l: .[0], n: length}) | sort_by(-.n)
    | map("\(.l) \(.n)") | join(", ")' "$out/lint.json")
  echo
  summary="${n} issues in ${files} files${by:+: ${by}}"

  # Quality is advisory in CI, as annotations: new code over a complexity
  # threshold becomes a warning on the pull request's diff, never an error.
  if [ -n "${2:-}" ] && git rev-parse --verify --quiet "$base^{commit}" >/dev/null; then
    cstatus=0
    golangci-lint run -c .github/ci/golangci-full.yml --enable-only=gocyclo,gocognit,funlen \
      --new-from-merge-base="$base" --output.text.path=stdout --output.json.path="$out/complexity.json" \
      --show-stats=false ./... || cstatus=$?
    if [ "$cstatus" -le 1 ] && jq -e . "$out/complexity.json" >/dev/null 2>&1; then
      cn=$(jq '.Issues // [] | length' "$out/complexity.json")
      if in_ci; then
        jq -r '(.Issues // [])[] | "::warning file=\(.Pos.Filename),line=\(.Pos.Line),title=\(.FromLinter)::\(.Text | gsub("\n"; " "))"' "$out/complexity.json"
      fi
      summary="${summary} · new code over a complexity threshold: ${cn}"
    else
      echo "lint: the new-code complexity check could not run (exit $cstatus); advisory, ignored" >&2
    fi
  fi
  report "$summary"
  ;;

*)
  echo "usage: $0 gate [base] | full" >&2
  exit 2
  ;;
esac
