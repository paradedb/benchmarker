#!/usr/bin/env bash
set -euo pipefail

# GO-2026-5932 affects every version of these deprecated packages.
# Inspect imported packages, including test dependencies, rather than modules.
for target_os in linux darwin; do
  for target_arch in amd64 arm64; do
    packages=$(GOOS="$target_os" GOARCH="$target_arch" go list -deps -test -f '{{.ImportPath}}' ./...)
    if grep -E '^golang\.org/x/crypto/openpgp(/|$)' <<< "$packages"; then
      echo "Deprecated OpenPGP dependency found for $target_os/$target_arch (GO-2026-5932)." >&2
      exit 1
    fi
    echo "No deprecated OpenPGP dependencies for $target_os/$target_arch."
  done
done
