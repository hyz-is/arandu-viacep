# Upgrade Guide

## v0.2.0

`go.mod` declares `go 1.26.10`. Every vulnerability an Arandu application
reaches is in the Go standard library (crypto/x509, the HTTP/2 and CONNECT
handling in net/http, html/template, net/textproto and crypto/tls) and is
fixed in Go 1.26.10, so a module that declared less let a build run on a
toolchain that still carries them. Build with Go 1.26.10 or later.

The package compiles against newer Arandu modules, and `arandu.mod.toml` now
declares `framework = ">= 0.66"`:

| | was | is |
|---|---|---|
| `github.com/arandu-io/framework` | `v0.50.2` | `v0.66.0` |
| `github.com/arandu-io/hesape` | `v0.44.3` | `v0.85.0` |

```bash
go get github.com/arandu-io/framework@v0.66.0
go get github.com/arandu-io/hesape@v0.85.0
```

An application below Framework v0.66.0 follows the Framework and Hesape
upgrade guides between the versions it had and these first. The route, its
answers and `Module.Lookup` are unchanged.

`apidiff` against `v0.1.0` names `(*Module).Routes` as changed, from
`func(*http.Router)` to `func(*http.Router)`: the framework's `http.Router` is
now an alias of Hesape's controller router, so the signature reads the same
and a caller compiles unchanged.
