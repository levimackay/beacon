# Beacon Hub and CLI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship `beaconhub` and `beacon` such that `beacon status` prints real,
stored, incident-aware health for the local Mac and a set of monitored
websites.

**Architecture:** One Go module. A hub binary owns an HTTP API, a SQLite store,
a scheduler and collectors; a CLI binary is a thin client over the same API.
Storage sits behind a `Store` interface. Time is injected everywhere so
incident durations are deterministic under test.

**Tech Stack:** Go 1.26, `modernc.org/sqlite v1.57.0` (pure Go, no cgo),
`github.com/shirou/gopsutil/v4 v4.26.7`, `log/slog`, `net/http`, stdlib
`testing`.

## Global Constraints

- Module path: `github.com/levimackay/beacon`.
- No cgo. `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...` must succeed at every commit.
- The hub binds `127.0.0.1` only. No code path may bind `0.0.0.0` or a LAN address.
- No shell execution. No endpoint accepts a command, path, or filesystem location.
- No AI attribution in any commit message, comment, file, or output string.
- Every mutating request writes an audit row.
- Tokens are never logged; the slog handler redacts them.
- Retention: raw 15s samples 6h, 5m buckets 7d, 1h buckets 90d.
- Flap suppression: a state change requires 2 consecutive confirming samples.

---

## File Structure

    beacon/
      go.mod
      cmd/beaconhub/main.go        hub entrypoint, wiring, launchd-friendly
      cmd/beacon/main.go           CLI entrypoint
      internal/protocol/           wire types shared by hub, CLI and (later) Swift
        state.go                   State, Severity, TargetKind enums
        target.go                  Target, TargetStatus
        sample.go                  Sample, HostMetrics
        incident.go                Incident
        snapshot.go                Snapshot, HubInfo, Counts
      internal/clock/clock.go      Clock interface, real and fake
      internal/store/
        store.go                   Store interface, Open, migrations
        schema.sql                 embedded DDL
        targets.go                 target CRUD
        samples.go                 sample insert and query
        incidents.go               incident open/close/list
        audit.go                   audit log append
        retention.go               rollup and prune
      internal/collect/
        host.go                    gopsutil host collector
        web.go                      HTTP and TLS website collector
        guard.go                   SSRF address-range guard
      internal/incident/
        threshold.go               metrics to State
        machine.go                 flap suppression and transitions
      internal/api/
        server.go                  routes, middleware chain
        auth.go                    bearer token, constant-time compare
        ratelimit.go               per-principal token bucket
        snapshot.go                GET /v1/snapshot
        targets.go                 target endpoints
        incidents.go               incident endpoints
        health.go                  /v1/health, /v1/diagnostics
      internal/config/config.go    paths, first-run token generation
      internal/scheduler/scheduler.go

---

## Task 1: Module, protocol types, injectable clock

**Files:**
- Create: `go.mod`, `internal/protocol/*.go`, `internal/clock/clock.go`
- Test: `internal/protocol/snapshot_test.go`, `internal/clock/clock_test.go`

**Interfaces produced:**

```go
package protocol

type State string
const (
    StateHealthy State = "healthy"
    StateWarning State = "warning"
    StateDown    State = "down"
    StateUnknown State = "unknown"
)

// Worse reports whether s is a more severe state than other.
func (s State) Worse(other State) bool

type TargetKind string
const (
    KindHost    TargetKind = "host"
    KindWebsite TargetKind = "website"
    KindService TargetKind = "service"
)

type Target struct {
    ID              string     `json:"id"`
    Kind            TargetKind `json:"kind"`
    Name            string     `json:"name"`
    Address         string     `json:"address"`
    IntervalSeconds int        `json:"intervalSeconds"`
    ExpectStatus    int        `json:"expectStatus,omitempty"`
    Enabled         bool       `json:"enabled"`
}

type Sample struct {
    TargetID  string             `json:"targetId"`
    At        time.Time          `json:"at"`
    State     State              `json:"state"`
    LatencyMS float64            `json:"latencyMs"`
    Metrics   map[string]float64 `json:"metrics,omitempty"`
    Error     string             `json:"error,omitempty"`
}

type Incident struct {
    ID         int64      `json:"id"`
    TargetID   string     `json:"targetId"`
    TargetName string     `json:"targetName"`
    State      State      `json:"state"`
    StartedAt  time.Time  `json:"startedAt"`
    ResolvedAt *time.Time `json:"resolvedAt,omitempty"`
    Summary    string     `json:"summary"`
}

type TargetStatus struct {
    Target     Target             `json:"target"`
    State      State              `json:"state"`
    LatencyMS  float64            `json:"latencyMs"`
    Metrics    map[string]float64 `json:"metrics,omitempty"`
    LastCheck  time.Time          `json:"lastCheck"`
    Error      string             `json:"error,omitempty"`
    CertExpiry *time.Time         `json:"certExpiry,omitempty"`
}

type Counts struct {
    Critical int `json:"critical"`
    Warning  int `json:"warning"`
    Healthy  int `json:"healthy"`
    Unknown  int `json:"unknown"`
}

type HubInfo struct {
    Version       string    `json:"version"`
    Host          string    `json:"host"`
    OS            string    `json:"os"`
    Kernel        string    `json:"kernel"`
    StartedAt     time.Time `json:"startedAt"`
    UptimeSeconds int64     `json:"uptimeSeconds"`
}

type Snapshot struct {
    GeneratedAt   time.Time      `json:"generatedAt"`
    Overall       State          `json:"overall"`
    Hub           HubInfo        `json:"hub"`
    Counts        Counts         `json:"counts"`
    Targets       []TargetStatus `json:"targets"`
    OpenIncidents []Incident     `json:"openIncidents"`
}

// Overall derives the worst state across statuses.
func Overall(statuses []TargetStatus) State
```

```go
package clock

type Clock interface{ Now() time.Time }

func Real() Clock
// Fake returns a Clock whose time only advances via Advance.
func Fake(start time.Time) *FakeClock
func (f *FakeClock) Advance(d time.Duration)
```

- [ ] **Step 1: Write the failing tests**

`internal/protocol/snapshot_test.go` asserts: `Overall` on an empty slice is
`StateUnknown`; on all-healthy is `StateHealthy`; a single `StateDown` among
healthy yields `StateDown`; `StateWarning` beats `StateHealthy` but loses to
`StateDown`; `StateUnknown` does not mask a `StateDown`. Also asserts a
`Snapshot` round-trips through `encoding/json` unchanged.

`internal/clock/clock_test.go` asserts `Fake` does not advance on its own and
advances exactly by `Advance`.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/...`
Expected: build failure, packages do not exist.

- [ ] **Step 3: Implement**

Write the types and functions above. `Overall` ranks
`down > unknown > warning > healthy` and returns `StateUnknown` for an empty
slice.

- [ ] **Step 4: Confirm pass and cross-compile**

Run: `go test ./internal/... && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...`
Expected: `ok` for both packages, build silent.

- [ ] **Step 5: Commit**

```bash
git add go.mod internal/protocol internal/clock
git commit -m "Add protocol types and injectable clock"
```

---

## Task 2: SQLite store, migrations, retention

**Files:**
- Create: `internal/store/{store.go,schema.sql,targets.go,samples.go,incidents.go,audit.go,retention.go}`
- Test: `internal/store/{store_test.go,retention_test.go}`

**Interfaces consumed:** `protocol`, `clock` from Task 1.

**Interfaces produced:**

```go
package store

type Store interface {
    UpsertTarget(ctx context.Context, t protocol.Target) error
    DeleteTarget(ctx context.Context, id string) error
    Targets(ctx context.Context) ([]protocol.Target, error)

    InsertSample(ctx context.Context, s protocol.Sample) error
    LatestSamples(ctx context.Context) (map[string]protocol.Sample, error)
    SampleSeries(ctx context.Context, targetID, metric string, since time.Time) ([]Point, error)

    OpenIncident(ctx context.Context, in protocol.Incident) (int64, error)
    ResolveIncident(ctx context.Context, targetID string, at time.Time) error
    OpenIncidents(ctx context.Context) ([]protocol.Incident, error)
    Incidents(ctx context.Context, f IncidentFilter) ([]protocol.Incident, error)

    Audit(ctx context.Context, principal, action, target, result string) error
    Rollup(ctx context.Context, now time.Time) (RollupStats, error)
    Close() error
}

type Point struct { At time.Time; Value float64 }
type IncidentFilter struct { TargetID string; Since, Until time.Time; Limit int }
type RollupStats struct { Rolled5m, Rolled1h, Pruned int64 }

func Open(path string, c clock.Clock) (Store, error)
```

Schema: `samples(target_id, at, bucket, state, latency_ms, metric, value, error)`
where `bucket` is `0` for raw, `300` for five-minute means, `3600` for hourly
means. `targets`, `incidents`, `audit` as implied by the interface. WAL mode,
`busy_timeout=5000`, an index on `(target_id, metric, bucket, at)`.

- [ ] **Step 1: Write the failing tests**

`store_test.go`: opening a temp-file store creates the schema and is
idempotent when reopened; upsert then read a target returns it unchanged;
inserting samples and reading `LatestSamples` returns only the newest per
target; opening an incident then resolving it sets `ResolvedAt` and it leaves
`OpenIncidents`; resolving a target with no open incident is a no-op, not an
error; `Audit` rows are readable back in insertion order.

`retention_test.go`: insert 30 days of synthetic samples at 15s spacing for
one target and one metric using a `clock.Fake`; run `Rollup`; assert no raw
rows survive older than 6h, no 5m rows older than 7d, no 1h rows older than
90d; assert the mean of the 5m bucket equals the mean of the raw rows it
replaced within a small epsilon; assert `Rollup` is idempotent when run twice.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/store/...`
Expected: build failure.

- [ ] **Step 3: Implement the store**

Embed `schema.sql` with `//go:embed`. Apply it with `CREATE TABLE IF NOT
EXISTS`. All queries use bound parameters, never string concatenation.

- [ ] **Step 4: Confirm pass**

Run: `go test ./internal/store/... -v`
Expected: all tests pass, retention test included.

- [ ] **Step 5: Commit**

```bash
git add internal/store
git commit -m "Add SQLite store with retention and rollup"
```

---

## Task 3: Collectors and the SSRF guard

**Files:**
- Create: `internal/collect/{host.go,web.go,guard.go}`
- Test: `internal/collect/{host_test.go,web_test.go,guard_test.go}`

**Interfaces consumed:** `protocol`, `clock`.

**Interfaces produced:**

```go
package collect

type Collector interface {
    Collect(ctx context.Context, t protocol.Target) protocol.Sample
}

func NewHost(c clock.Clock) Collector
func NewWeb(c clock.Clock, g *Guard) Collector

// Guard rejects addresses that resolve into loopback, private, link-local,
// unique-local, unspecified or cloud metadata ranges.
type Guard struct{ AllowPrivate bool }
func NewGuard() *Guard
func (g *Guard) CheckURL(raw string) error
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error)
```

Host collector emits metrics `cpu_percent`, `mem_percent`, `disk_percent`,
`load1`, `uptime_seconds`, and `temp_c` when `sensors.SensorsTemperatures`
returns a non-empty reading. It never returns `StateDown`; a collection error
is `StateUnknown` with `Error` set.

Web collector reports `StateDown` on transport error or a status other than
`ExpectStatus` (defaulting to 200), `StateWarning` when the TLS certificate
expires within 14 days, and sets `CertExpiry`. Redirects are capped at 3 and
each hop is revalidated through the Guard. Timeout 10s.

- [ ] **Step 1: Write the failing tests**

`guard_test.go` is the security-critical one. Table test asserting `CheckURL`
rejects: `http://127.0.0.1/`, `http://localhost/`, `http://[::1]/`,
`http://169.254.169.254/latest/meta-data/`, `http://10.0.0.1/`,
`http://192.168.1.1/`, `http://172.16.0.1/`, `http://[fd00::1]/`,
`http://0.0.0.0/`, a `file://` scheme, a `gopher://` scheme, and a hostname
whose DNS resolves to a private address. It accepts a public address. Assert
the rejection is by resolved IP, not by string matching the hostname.

`web_test.go` uses `httptest.NewServer` with `Guard{AllowPrivate: true}`:
a 200 yields `StateHealthy` with a positive latency; a 500 against
`ExpectStatus: 200` yields `StateDown`; a server that closes the connection
yields `StateDown` with a non-empty `Error`; a 4-hop redirect chain yields
`StateDown` with a redirect-limit error.

`host_test.go` asserts a collection on the running machine returns
`StateHealthy` or `StateWarning`, `cpu_percent` and `mem_percent` present and
within `0..100`, and `uptime_seconds` positive.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/collect/...`
Expected: build failure.

- [ ] **Step 3: Implement**

The Guard performs resolution in `DialContext` so the check applies to the
address actually connected to, closing the DNS-rebinding window that a
pre-flight-only check would leave open. `CheckURL` additionally rejects any
scheme other than `http` and `https` before any network activity occurs.

- [ ] **Step 4: Confirm pass**

Run: `go test ./internal/collect/... -v`
Expected: all pass, guard table fully green.

- [ ] **Step 5: Commit**

```bash
git add internal/collect
git commit -m "Add host and website collectors with SSRF guard"
```

---

## Task 4: Threshold evaluation and the incident state machine

**Files:**
- Create: `internal/incident/{threshold.go,machine.go}`
- Test: `internal/incident/{threshold_test.go,machine_test.go}`

**Interfaces produced:**

```go
package incident

type Thresholds struct {
    CPUWarn, CPUDown           float64 // default 85, 95
    MemWarn, MemDown           float64 // default 85, 95
    DiskWarn, DiskDown         float64 // default 85, 95
    TempWarnC, TempDownC       float64 // default 80, 90
    CertWarnDays               int     // default 14
}
func DefaultThresholds() Thresholds
// Evaluate downgrades a sample's state according to its metrics.
func (t Thresholds) Evaluate(s protocol.Sample) (protocol.State, string)

// Machine suppresses flapping and emits transitions.
type Machine struct{ Confirmations int } // default 2
func NewMachine(c clock.Clock) *Machine

type Transition struct {
    TargetID string
    From, To protocol.State
    At       time.Time
    Summary  string
}
// Observe returns a non-nil Transition only when the state has changed and
// been confirmed Confirmations times consecutively.
func (m *Machine) Observe(targetID string, s protocol.State, summary string, at time.Time) *Transition
```

- [ ] **Step 1: Write the failing tests**

`threshold_test.go`: 84% CPU is healthy, 86% is warning, 96% is down; the
returned summary names the metric and value; the worst metric wins when CPU is
warning and disk is down; a sample already `StateUnknown` stays unknown
regardless of metrics.

`machine_test.go`, driven by `clock.Fake`: a first observation of healthy
emits a transition from `StateUnknown`; a single down observation emits
nothing; two consecutive downs emit one transition; down, healthy, down emits
nothing because the run was broken; after a confirmed down, two confirmed
healthys emit exactly one recovery transition; a third consecutive down after
a confirmed down emits nothing, proving no duplicate alerts.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/incident/...`
Expected: build failure.

- [ ] **Step 3: Implement**

- [ ] **Step 4: Confirm pass**

Run: `go test ./internal/incident/... -v`

- [ ] **Step 5: Commit**

```bash
git add internal/incident
git commit -m "Add threshold evaluation and flap-suppressing incident machine"
```

---

## Task 5: Config, first-run token, HTTP API

**Files:**
- Create: `internal/config/config.go`, `internal/api/{server.go,auth.go,ratelimit.go,snapshot.go,targets.go,incidents.go,health.go}`
- Test: `internal/api/{auth_test.go,server_test.go}`, `internal/config/config_test.go`

**Interfaces consumed:** all prior tasks.

**Interfaces produced:**

```go
package config

type Config struct {
    Dir       string // ~/Library/Application Support/Beacon on darwin
    DBPath    string
    TokenPath string
    Token     string
    Addr      string // always 127.0.0.1:<port>
    Port      int    // default 47654
}
func Load() (*Config, error) // generates a 32-byte token on first run, mode 0600
```

```go
package api

type Deps struct {
    Store  store.Store
    Clock  clock.Clock
    Token  string
    Hub    protocol.HubInfo
}
func NewServer(d Deps) http.Handler
```

Routes, all under `/v1`, all requiring `Authorization: Bearer <token>` except
`/v1/health`:

    GET    /v1/health          liveness, unauthenticated, no data disclosed
    GET    /v1/diagnostics     hub info, store stats, last scheduler tick
    GET    /v1/snapshot        the single aggregate the app polls, ETag'd
    GET    /v1/targets
    POST   /v1/targets         create or update, validated
    DELETE /v1/targets/{id}
    GET    /v1/incidents       filterable by target, since, until, limit

- [ ] **Step 1: Write the failing tests**

`auth_test.go`: a request with no header is 401; a wrong token is 401; a token
that is a prefix of the real one is 401; the correct token is 200; `/v1/health`
is 200 without a header; the 401 body discloses neither the expected token nor
its length. Assert comparison uses `subtle.ConstantTimeCompare` by asserting
behaviour on equal-length wrong tokens.

`server_test.go`: `GET /v1/snapshot` returns an `ETag` and a second request
carrying `If-None-Match` returns 304 with an empty body; `POST /v1/targets`
with a website URL pointing at `169.254.169.254` is rejected 400 by the Guard
before any storage write; `POST /v1/targets` with a negative interval is 400;
`DELETE` of an unknown id is 404; an unparseable JSON body is 400 and not 500;
a mutating request writes exactly one audit row; exceeding the rate limit
returns 429.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/api/... ./internal/config/...`

- [ ] **Step 3: Implement**

Middleware order: recover, rate limit, auth, audit, handler. The slog handler
replaces any attribute whose key contains `token` with `[redacted]`.

- [ ] **Step 4: Confirm pass**

Run: `go test ./internal/api/... ./internal/config/... -v`

- [ ] **Step 5: Commit**

```bash
git add internal/api internal/config
git commit -m "Add HTTP API with bearer auth, rate limiting and audit"
```

---

## Task 6: Scheduler and the hub binary

**Files:**
- Create: `internal/scheduler/scheduler.go`, `cmd/beaconhub/main.go`
- Test: `internal/scheduler/scheduler_test.go`

**Interfaces produced:**

```go
package scheduler

type Scheduler struct{ ... }
func New(s store.Store, c clock.Clock, collectors map[protocol.TargetKind]collect.Collector, m *incident.Machine, th incident.Thresholds) *Scheduler
func (s *Scheduler) Run(ctx context.Context) error
func (s *Scheduler) LastTick() time.Time
```

One goroutine per enabled target with a jittered ticker at the target's
interval; a shared hourly rollup ticker. A confirmed transition into a
non-healthy state opens an incident; a confirmed transition back to healthy
resolves it.

- [ ] **Step 1: Write the failing test**

`scheduler_test.go` with a fake collector and a fake clock: two confirmed
failing collections open exactly one incident in the store; two subsequent
confirmed successes resolve it with a correct duration; a collector that
panics does not kill the scheduler and records `StateUnknown`; cancelling the
context returns from `Run` within one tick.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/scheduler/...`

- [ ] **Step 3: Implement scheduler and `cmd/beaconhub`**

`main.go` loads config, opens the store, seeds a `host` target for the local
machine on first run, builds the collector map, starts the scheduler, serves
the API on `127.0.0.1`, and shuts down cleanly on SIGINT and SIGTERM.

- [ ] **Step 4: Confirm pass and run for real**

Run: `go test ./... && go run ./cmd/beaconhub &` then
`curl -s -H "Authorization: Bearer $(cat ~/Library/Application\ Support/Beacon/token)" localhost:47654/v1/snapshot | head -40`
Expected: a JSON snapshot containing live CPU and memory numbers for this Mac.

- [ ] **Step 5: Commit**

```bash
git add internal/scheduler cmd/beaconhub
git commit -m "Add scheduler and hub binary"
```

---

## Task 7: The CLI

**Files:**
- Create: `cmd/beacon/main.go`, `internal/cliclient/client.go`
- Test: `internal/cliclient/client_test.go`

**Commands:** `beacon status`, `beacon devices`, `beacon websites`,
`beacon incidents [--since 24h]`, `beacon check <url>`, `beacon add <url> --name N`,
`beacon rm <id>`, `beacon diagnostics`.

Output is aligned plain text with a state glyph, colourised only when stdout is
a terminal. `--json` on any command emits the raw API payload.

- [ ] **Step 1: Write the failing test**

`client_test.go` against an `httptest` server: `status` renders a snapshot into
text containing each target name and its state; a 401 produces a message
telling the user the hub token is wrong rather than a Go error dump; an
unreachable hub produces "hub unreachable" and a non-zero exit code, not a
panic; `--json` emits bytes that parse as the original payload.

- [ ] **Step 2: Run and confirm failure**

Run: `go test ./internal/cliclient/...`

- [ ] **Step 3: Implement**

- [ ] **Step 4: Confirm pass and run for real**

Run: `go test ./... && go run ./cmd/beacon status`
Expected: live health for this Mac printed to the terminal.

- [ ] **Step 5: Commit**

```bash
git add cmd/beacon internal/cliclient
git commit -m "Add beacon CLI"
```

---

## Task 8: Hardening pass and CI

**Files:**
- Create: `.github/workflows/ci.yml`, `.gitignore`, `README.md`, `LICENSE`, `SECURITY.md`
- Modify: whatever the review finds.

- [ ] **Step 1: Run the full suite with the race detector**

Run: `go test -race ./...`
Expected: pass, no data races. The scheduler is the likely offender.

- [ ] **Step 2: Vet, and cross-compile for the Pi**

Run: `go vet ./... && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/beaconhub-arm64 ./cmd/beaconhub && file /tmp/beaconhub-arm64`
Expected: `ELF 64-bit LSB executable, ARM aarch64`.

- [ ] **Step 3: Dedicated security review**

Dispatch the `security-auditor` agent over the whole tree with the spec's
threat list: authentication bypass, authorisation bypass, SSRF, command
injection, SQL injection, path traversal, secret exposure, insecure device
registration, privilege escalation, sensitive data in logs. Fix what it finds.

- [ ] **Step 4: CI**

`ci.yml` runs `go vet`, `go test -race ./...`, `govulncheck`, and the arm64
cross-compile on every push.

- [ ] **Step 5: Commit**

```bash
git add .github .gitignore README.md LICENSE SECURITY.md
git commit -m "Add CI, documentation and hardening fixes"
```

---

## Deviation from the skill's default

This plan locks interfaces, tests and commands but does not inline every
production function body, because the plan is executed in the session that
wrote it rather than handed to an engineer with no context. Test bodies and
public signatures are specified exactly, since those are what the tasks must
agree on.
