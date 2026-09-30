# Dependency security

## Deprecated OpenPGP packages (GO-2026-5932)

[GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932) affects
`golang.org/x/crypto/openpgp` and its subpackages in all versions, with no
known fixed version. Updating `golang.org/x/crypto` cannot resolve this advisory.

Benchmarker does not import these packages, including through its test and
transitive dependencies on Linux and macOS. It requires other packages from
`golang.org/x/crypto`, so removing or replacing the entire module is unnecessary.
A scanner that flags only the module's presence in `go.mod` may report this
advisory even when the affected packages are absent from the build.

CI runs `bash scripts/check-no-openpgp.sh` to verify the dependency graphs for
Linux and macOS on amd64 and arm64, and rejects imports of any affected package.
The check fails if Go cannot resolve the dependency graph.

For Oneleet findings against `go.mod`, use the package graph check and the
advisory's affected package list as evidence to mark this specific finding as
not applicable. This does not exempt other `golang.org/x/crypto` advisories.
If OpenPGP support becomes necessary, use a maintained implementation such as
the advisory's recommended `github.com/ProtonMail/go-crypto/openpgp`.
