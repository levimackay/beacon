# Contributing

This is a small, single-maintainer project, so the process is short on
purpose.

## Building

```
go build ./...
```

`CGO_ENABLED=0` at all times; the SQLite driver (`modernc.org/sqlite`) is
pure Go specifically so cross-compiling to the Raspberry Pi never needs a C
toolchain. Confirm that target still builds before sending a change:

```
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...
```

## Testing

```
go test -race ./...
```

Always with `-race`. The scheduler and the incident state machine are
shared across goroutines by design, and a data race there is exactly the
kind of bug that only shows up under load, on someone else's machine.

New logic needs a test. That includes anything touching the SSRF guard, the
incident state machine, retention, or the API's auth and validation paths;
those are the places where an untested change turns into a security bug or
a silent data-loss bug rather than a crash you'd notice.

Run `go vet ./...` before sending a change; CI runs it too, but it is fast
enough to run locally first.

## Style

`gofmt` the code (`gofmt -l .` should print nothing). Beyond that, match
what is already in the package you are editing: table-driven tests, an
injected `clock.Clock` instead of `time.Now()` in anything whose behavior
depends on time, typed request/response models in the API rather than
`map[string]any`, and no shell execution anywhere, ever.

## Structure

- `internal/protocol`: wire types shared by the hub, the CLI, and later
  the Swift clients. Changing a field here is a wire-format change; treat
  it accordingly.
- `internal/store`: the SQLite store, behind the `Store` interface. Prefer
  extending the interface over reaching around it.
- `internal/collect`: the host and website collectors and the SSRF guard.
  Any change to `guard.go` should assume it is security-critical, because
  it is.
- `internal/incident`: threshold evaluation and the flap-suppressing
  state machine.
- `internal/api`, `internal/scheduler`, `cmd/beaconhub`, `cmd/beacon`: the
  server, the scheduler, and the two binaries.

## Sending a change

Small, focused pull requests over large ones. Describe what changed and
why in the PR description; the "why" matters more than a restatement of
the diff. `main` requires a passing CI run and a review before merge (see
the protected-branch setup at the bottom of the README).
