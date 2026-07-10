module github.com/example/token-guard

go 1.23

// No Tyk require is pinned here on purpose. The plugin compiler (Makefile:
// GO_GET=1) runs `go get github.com/TykTechnologies/tyk@<gateway-commit>` and
// builds against the compiler image's bundled gateway source via a go workspace,
// which guarantees the plugin ABI matches the gateway exactly.
