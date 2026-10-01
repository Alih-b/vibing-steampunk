#!/usr/bin/env bash
# fix-proven.sh: do the tests a change adds actually fail without its fix?
#
#   ./.github/ci/fix-proven.sh [base]        # base defaults to origin/main
#
# Takes the commit at HEAD and its merge base with <base>. The Go test functions
# HEAD added or changed are run against a scratch copy of HEAD in which every
# non-test .go file HEAD changed is put back as it was at the merge base (files
# HEAD added are removed). Test files, testdata and non-Go files stay as HEAD
# has them. A new test that fails there, or does not compile there, is a test
# that proves the fix.
#
# Advisory, so it always exits 0. Only committed state is looked at: commit
# first if you run it locally. Under GitHub Actions the verdict is also emitted
# as a `ci-report` annotation, which ci-report.yml shows in the PR comment.
set -uo pipefail

base_ref=${1:-origin/main}
head=HEAD

verdict() {
  echo
  echo "fix proven: $1"
  if [ "${GITHUB_ACTIONS:-}" = "true" ]; then
    echo "::notice title=ci-report::$1"
  fi
  exit 0
}

repo=$(git rev-parse --show-toplevel 2>/dev/null) || verdict "not run: not a git repository"
cd "$repo" || verdict "not run: cannot enter $repo"
git rev-parse --verify -q "$base_ref^{commit}" >/dev/null || verdict "not run: unknown base $base_ref"
base=$(git merge-base "$base_ref" "$head") || verdict "not run: no merge base with $base_ref"
echo "head $(git rev-parse --short "$head"), merge base with $base_ref $(git rev-parse --short "$base")"

work=$(mktemp -d "${TMPDIR:-/tmp}/fix-proven.XXXXXX") || verdict "not run: mktemp failed"
trap 'rm -rf "$work"' EXIT

# --- what changed -------------------------------------------------------------
# --no-renames: a moved file is a delete plus an add, which restores cleanly.
git diff --name-status --no-renames "$base" "$head" -- '*.go' > "$work/changes" \
  || verdict "not run: git diff failed"

: > "$work/src"    # "<status> <path>" for non-test .go files
: > "$work/tests"  # paths of *_test.go files HEAD added or modified
while IFS=$'\t' read -r status path; do
  case "$path" in
    */testdata/*|testdata/*) ;;                     # fixtures go with the tests
    *_test.go) [ "$status" != D ] && echo "$path" >> "$work/tests" ;;
    *) echo "$status $path" >> "$work/src" ;;
  esac
done < "$work/changes"

# --- which tests --------------------------------------------------------------
# A Test/Fuzz function counts when a line inside it (declaration to closing
# brace, gofmt layout) is added or changed. A pure deletion counts when the
# lines on both sides of it are inside the same function.
: > "$work/targets"  # "<dir> <TestName>"
while read -r f; do
  git diff -U0 --no-renames "$base" "$head" -- "$f" | awk '
    /^@@/ {
      match($0, /\+[0-9]+(,[0-9]+)?/)
      n = split(substr($0, RSTART + 1, RLENGTH - 1), a, ",")
      start = a[1] + 0; cnt = (n > 1) ? a[2] + 0 : 1
      if (cnt == 0) print "gap", start
      else for (i = 0; i < cnt; i++) print "line", start + i
    }' > "$work/touched"
  git show "$head:$f" | awk -v dir="$(dirname "$f")" -v touched="$work/touched" '
    BEGIN {
      while ((getline l < touched) > 0) {
        split(l, p, " ")
        if (p[1] == "line") line[p[2]] = 1; else gap[p[2]] = 1
      }
    }
    function check(   i) {
      for (i = from; i <= NR; i++) if (line[i] || (gap[i] && i < NR)) { print dir, name; return }
    }
    /^func (Test|Fuzz)([A-Z0-9_][A-Za-z0-9_]*)?\(/ {
      name = substr($0, 6); sub(/\(.*/, "", name)
      from = NR; infunc = (name != "TestMain")
      if (infunc && $0 ~ /}[ \t]*$/) { check(); infunc = 0 }
      next
    }
    infunc && /^}/ { check(); infunc = 0 }
  ' >> "$work/targets"
done < "$work/tests"
sort -u -o "$work/targets" "$work/targets"

nsrc=$(wc -l < "$work/src" | tr -d ' ')
ntests=$(wc -l < "$work/targets" | tr -d ' ')
echo "non-test Go files changed: $nsrc; new or changed tests: $ntests"
[ "$nsrc" -gt 0 ] && sed 's/^/  src   /' "$work/src"
[ "$ntests" -gt 0 ] && sed 's/^/  test  /' "$work/targets"
if [ "$nsrc" -eq 0 ] || [ "$ntests" -eq 0 ]; then
  verdict "no fix to prove"
fi

# --- the scratch copy: HEAD's tests on the base's code --------------------------
tree="$work/tree"
mkdir -p "$tree"
git archive "$head" | tar -x -C "$tree" || verdict "not run: git archive failed"
while read -r status path; do
  if [ "$status" = A ]; then
    rm -f "$tree/$path"
  else
    mkdir -p "$tree/$(dirname "$path")"
    git show "$base:$path" > "$tree/$path" || verdict "not run: cannot read $path at base"
  fi
done < "$work/src"

# --- run them, one package at a time --------------------------------------------
failed=0 broken=0 passed=0 skipped=0
cut -d' ' -f1 "$work/targets" | sort -u > "$work/dirs"
while IFS= read -r dir <&3; do
  names=$(awk -v d="$dir" '$1 == d { print $2 }' "$work/targets" | paste -sd'|' -)
  echo
  echo "== ./$dir: go test -run '^($names)\$' (on base code)"
  (cd "$tree" && go test -count=1 -vet=off -json -run "^($names)\$" "./$dir") > "$work/out.json" 2> "$work/err.txt"
  # Show what the go tool said, minus the chatter of tests that passed.
  jq -rj 'select(.Action == "output" or .Action == "build-output") | .Output' "$work/out.json" 2>/dev/null \
    | grep -v -E '^(=== (RUN|PAUSE|CONT)|--- PASS|PASS$|ok )' | head -n 60
  head -n 20 "$work/err.txt"
  pkgfail=$(jq -rs 'any(.[]; .Test == null and .Action == "fail")' "$work/out.json" 2>/dev/null) || pkgfail=true
  for name in ${names//|/ }; do
    result=$(jq -rs --arg t "$name" '
      (map(select(.Test == $t and (.Action == "pass" or .Action == "fail" or .Action == "skip"))) | last | .Action) // "none"' \
      "$work/out.json" 2>/dev/null) || result=none
    case "$result" in
      fail) failed=$((failed + 1)); echo "  FAIL on base   $name" ;;
      pass) passed=$((passed + 1)); echo "  pass on base   $name   (does not prove the fix)" ;;
      skip) skipped=$((skipped + 1)); echo "  skip on base   $name" ;;
      *)    if [ "$pkgfail" = false ]; then
              skipped=$((skipped + 1)); echo "  not run        $name   (no result; is the name right?)"
            else
              broken=$((broken + 1)); echo "  no build/run   $name   (does not compile or run on base)"
            fi ;;
    esac
  done
done 3< "$work/dirs"

proven=$((failed + broken))
echo
echo "reproduce with: ./.github/ci/fix-proven.sh $base_ref"
if [ "$proven" -gt 0 ]; then
  detail=""
  if [ "$broken" -gt 0 ]; then
    detail=" ($broken do not compile)"
    [ "$failed" -gt 0 ] && detail=" ($failed fail, $broken do not compile)"
  fi
  verdict "$proven/$ntests new tests fail on base (fix proven)$detail"
fi
verdict "0/$ntests new tests fail on base — tests do not prove the fix"
