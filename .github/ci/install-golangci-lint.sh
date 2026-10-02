#!/usr/bin/env bash
# install-golangci-lint.sh <dir>: the pinned golangci-lint release binary for
# linux-amd64, checked against the SHA-256 pinned in ci.yml before it is
# unpacked. Both come from the environment, so a version bump is one edit there:
#   GOLANGCI_LINT_VERSION   e.g. v2.13.2
#   GOLANGCI_LINT_SHA256    of golangci-lint-<version>-linux-amd64.tar.gz, from
#                           the release's checksums.txt
set -euo pipefail

dir=${1:?usage: $0 <install dir>}
v=${GOLANGCI_LINT_VERSION:?GOLANGCI_LINT_VERSION is not set}
v=${v#v}
sum=${GOLANGCI_LINT_SHA256:?GOLANGCI_LINT_SHA256 is not set}
name=golangci-lint-${v}-linux-amd64

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
curl -sSfL --retry 3 -o "$tmp/$name.tar.gz" \
  "https://github.com/golangci/golangci-lint/releases/download/v${v}/${name}.tar.gz"
echo "${sum}  $tmp/$name.tar.gz" | sha256sum -c --quiet -
tar -xzf "$tmp/$name.tar.gz" -C "$tmp" "$name/golangci-lint"
mkdir -p "$dir"
install -m 0755 "$tmp/$name/golangci-lint" "$dir/golangci-lint"
"$dir/golangci-lint" version
