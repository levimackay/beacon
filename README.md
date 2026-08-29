# Beacon

Beacon is a personal infrastructure monitor: one program that tells you,
at a glance, whether your own machines, services and websites are healthy.
The goal is that you should not have to become a DevOps engineer to
monitor your own hardware: no database server to run, no reverse proxy to
configure, no YAML file to hand-edit before you can see whether your Mac
is under disk pressure or your website is up.

Beacon is built for one person watching their own stuff over their own
tailnet. It is not a multi-tenant monitoring platform.

## Status

This section is deliberately plain about what runs today versus what is
still a design decision on paper. The architecture and the reasoning behind
it are in [`docs/design.md`](docs/design.md).

Built and tested:

- A SQLite store with retention: raw 15 second samples for 6 hours, rolled
  up into 5 minute buckets for 7 days, then 1 hour buckets for 90 days,
  plus an append-only audit log.
- A host metrics collector (CPU, memory, disk, load average, uptime,
  temperature where the platform exposes it), built on `gopsutil`, so it
  runs on macOS and Linux without any hand-written `/proc` parsing.
- A website collector: HTTP status, response time and TLS certificate
  expiry, behind the SSRF guard described in Security model below. A
  target can add an expected body substring, so a 200 response with a
  blank, defaced, or parked-domain page is no longer reported healthy,
  and a latency threshold that reports warning rather than down for a
  site that is merely slow.
- Threshold evaluation and a flap-suppressing incident state machine: a
  state change needs two consecutive confirming samples before it opens
  or escalates an incident, and three before it closes one (closing is
  held to a higher bar on purpose; see `docs/design.md`).
- Network outage detection: when every website target fails at once while
  a local host or service check keeps succeeding, Beacon raises one
  incident against the network rather than one against every site.
- The HTTP API, bearer auth, split request and authentication rate
  limits, and the scheduler that ties the collectors, the store and the
  incident machine together on a timer.
- The `beaconhub` binary, and installing it as a per-user launchd agent
  so monitoring survives quitting a terminal and restarts at login.
- The macOS menu bar app and the WidgetKit widget (small, medium and
  large), in `apps/macos`. See `apps/macos/README.md`.
- The `beacon` CLI (`status`, `devices`, `websites`, `services`,
  `incidents`, `add`, `rm`, `diagnostics`, `version`).
- First-run configuration: token generation, the support directory, and
  the loopback listen address.

Planned, and explicitly not built:

- The web dashboard
- GitHub integration
- Docker monitoring
- Multi-user accounts
- The Raspberry Pi installer and systemd unit
- `tailscale serve` transport (today the hub is loopback-only; see
  Architecture)

Each of those is a planned addition to the current architecture, not a
redesign of it.

## Quick start

Build both binaries:

    go build -o bin/beaconhub ./cmd/beaconhub
    go build -o bin/beacon ./cmd/beacon

Run the hub:

    ./bin/beaconhub

On first run this creates the support directory (`~/Library/Application
Support/Beacon` on macOS, overridable with `BEACON_DIR`), generates a
bearer token at `<dir>/token` with permissions `0600`, and opens
`<dir>/beacon.db`. The hub listens on `127.0.0.1:47654` by default;
set `BEACON_PORT` to use a different port.

In another terminal:

    ./bin/beacon status

On a Mac with nothing else configured, that prints something like:

      Beacon  everything is healthy

      Devices
      ● your-mac  cpu 22%  mem 72%  disk 83%  52°C  up 17d 21h

      1 healthy · updated 0s ago

The hub seeds a target for the machine it runs on at first start, so
there is always something to report rather than an empty screen.

The CLI reads the same `BEACON_DIR`/`BEACON_PORT` and the token file the
hub just wrote, so as long as both processes agree on `BEACON_DIR` there
is nothing else to configure. `beacon status` is also what running
`beacon` with no arguments does.

Add a website to watch:

    ./bin/beacon add https://example.com --name "Example" --every 60s --expect 200

`--contains TEXT` additionally requires that text to appear somewhere in
the response body, so a 200 that comes back blank or defaced is reported
down instead of healthy. `--warn-after 2s` reports warning rather than
down when a response is slower than that, instead of leaving a merely
slow site indistinguishable from a healthy one. Both are optional.

A site that is currently unreachable can still be added: being down is
the condition you are asking Beacon to watch for, so it is accepted and
reported as down rather than refused.

Other commands: `beacon devices`, `beacon websites`, `beacon services`,
`beacon incidents --since 24h`, `beacon rm <id>`, `beacon diagnostics`.
Add `--json` to any command for the raw API payload instead of rendered
text, or `--no-color` to turn off ANSI color (also respected via the
`NO_COLOR` environment variable).

## The Mac app and widget

    ./scripts/install-macos.sh

That builds, signs and installs `Beacon.app`, then launches it. Add the
widget by right-clicking the desktop, choosing Edit Widgets, and searching
for Beacon. macOS has no way to place a widget programmatically, so that
last step is a gesture only you can make.

The app lives in the menu bar and has no Dock icon. Its icon carries the
overall state, so the answer to "is everything okay" is there without a
click. Clicking opens a compact panel; the full window is the same
information with room to breathe.

Requires [XcodeGen](https://github.com/yonaskolb/XcodeGen)
(`brew install xcodegen`) and an Apple Development signing identity, which
Xcode creates for you when you add your Apple ID under Settings, Accounts.
The widget will not register without one: macOS requires a widget
extension to be sandboxed and signed by a real team, and refuses silently
when it is not.

### Keeping the hub running

Running `beaconhub` in a terminal stops when the terminal does. To keep
monitoring going, install it as a per-user launchd agent:

    ./bin/beaconhub install     # installs and starts it, and starts it again at login
    ./bin/beaconhub status      # is the agent installed and running
    ./bin/beaconhub uninstall   # stops and removes it, leaving your data alone

This is a user agent in `~/Library/LaunchAgents`, not a system daemon:
no root, no `sudo`, no installer package. The trade is that it stops when
you log out. A system daemon would survive that, but it would need a
privileged install, which is the kind of setup friction this project
exists to remove. When the hub moves to a Raspberry Pi, systemd takes
over this role and the question stops being the Mac's problem.

While the hub runs on a Mac, monitoring pauses when that Mac sleeps, so
an overnight outage is detected at wake rather than as it happens. That
is a property of where the hub is deployed, not of the code, and it goes
away when the hub moves to hardware that stays awake.

## Architecture

### One binary, not a service mesh

Beacon is one Go binary, `beaconhub`, containing the HTTP API, the SQLite
store, the scheduler, the collectors and the incident engine. An earlier
version of the design split this into separate `api`, `worker` and
`agent` services; that adds a database server, a reverse proxy and a set
of migrations to run before you can see whether your own laptop is
healthy, which is exactly the burden this project exists to remove. A
single monitored machine does not need a distributed system.

A separate, slimmer agent process only earns its existence once there
are three or more monitored machines. Until then, the hub monitors the
host it runs on directly. The plan for the Raspberry Pi is to
cross-compile the identical binary for `linux/arm64` and run it there
instead, then point the Mac app at it; nothing else about the
architecture changes when that happens.

### Transport: loopback today, a tailnet later

Two transport modes are designed; one is built.

- **Loopback (built).** The hub binds `127.0.0.1` only.
  `internal/config.Config.Addr` always returns a loopback address, and
  there is no code path anywhere in the hub that binds `0.0.0.0` or a LAN
  interface. This is enforced by what the code can do, not just
  documented as a rule to follow.
- **Tailnet (planned, not implemented).** The intent is to publish the
  hub with `tailscale serve`, which would supply a real HTTPS
  certificate, a stable `https://<host>.<tailnet>.ts.net` URL, and
  caller identity from Tailscale itself. Because the network would
  already authenticate the caller, Beacon still would not need a login
  screen or a session store; it would only need to map an
  already-authenticated identity to a role, which is an addition to the
  current request pipeline rather than a rewrite of it.

### Data

Everything lives in one SQLite file (`<BEACON_DIR>/beacon.db`), opened
with `modernc.org/sqlite`, a pure-Go driver with no cgo dependency. That
keeps `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build` free of a C
toolchain, which matters because that exact command is how the
Raspberry Pi build gets produced. Storage sits behind a `Store` interface
(`internal/store.Store`), so swapping the backend later, if that is ever
genuinely needed, does not touch anything above it.

Retention runs in three tiers so the database stays bounded in size on a
Raspberry Pi's storage: raw 15 second samples for 6 hours, rolled up into
5 minute buckets for 7 days, then into 1 hour buckets for 90 days.
Rollup and pruning happen together in one pass, which only folds a time
window once it lies entirely past its retention cutoff, so a window is
never split across two aggregate rows (and a graph never shows the same
window twice).

## Security model

Beacon's security model rests on three things: the hub is only ever
reachable on an address you already trust, every request still needs a
credential, and anything that dials an address the operator does not
directly control is treated as hostile input until proven otherwise.

**Loopback-only bind.** The hub's listen address is always
`127.0.0.1:<port>` (`internal/config.Config.Addr`). There is no
configuration flag, environment variable, or code path that binds
`0.0.0.0` or any routable interface. Remote access is Tailscale's job,
not the hub's.

**Bearer token.** On first run, the hub generates a 32-byte random token
(`crypto/rand`), writes it to `<BEACON_DIR>/token` with permissions
`0600`, and tightens the file back to `0600` on every subsequent run if
something else has loosened it. The token is compared with a
constant-time comparison (hashed with SHA-256, then compared via
`crypto/subtle.ConstantTimeCompare`), so neither a wrong token's length
nor its content leaks through response timing. A missing header, a
malformed scheme, and a wrong token all produce the identical
`{"error":"unauthorized"}` body, so a caller can't distinguish "no
token" from "wrong token" by the response shape.

**The SSRF guard.** A website target is a URL the operator supplies, and
without a guard that is a way to make the hub fetch an arbitrary address,
including the cloud metadata endpoint or the hub's own loopback
interface. `internal/collect/guard.go` resolves the target's DNS and
rejects loopback, private (RFC 1918), carrier-grade NAT (RFC 6598,
100.64.0.0/10, which is also where Tailscale addresses live), link-local,
IPv6 unique-local, unspecified and multicast addresses by default. A
target can opt in per-target (`AllowPrivate`) to reaching the operator's
own private networks, for monitoring something on your own LAN or
tailnet, and that opt-in is checked before the request is made, not after
a failure. The cloud metadata address (`169.254.169.254`) is refused even
with `AllowPrivate` set: no legitimate monitoring target needs it, and
its exposure is exactly how an SSRF turns into a stolen credential.

The guard judges an address by what it actually reaches, not by how it is
written. An IPv6 address that carries an IPv4 address inside it is
unwrapped and the embedded address is checked: IPv4-mapped
(`::ffff:169.254.169.254`), IPv4-compatible (`::169.254.169.254`), the
NAT64 prefixes from RFC 6052 and RFC 8215 (`64:ff9b::a9fe:a9fe`, which
reaches the metadata endpoint on any DNS64/NAT64 network), 6to4
(`2002:a9fe:a9fe::`) and Teredo. Only the IPv4-mapped form is folded by
Go's standard library, so the rest would otherwise be unguarded routes to
precisely the addresses the guard exists to refuse.

Non-canonical numeric addresses are refused outright rather than
resolved: octal (`0177.0.0.1`), bare integers (`2130706433`),
hexadecimal (`0x7f000001`) and shortened dotted forms (`127.1`). Whether
those resolve, and to what, is decided by the resolver the binary was
linked against, and a static `CGO_ENABLED=0` build for the Raspberry Pi
uses a different one than a macOS build. A guard whose behaviour depends
on the C library it was linked against is not a guard, so Beacon requires
a canonical address. CI runs the guard's tests under both resolvers to
keep that from drifting.

The address check is repeated at dial time against the address actually
being connected to, not only against what DNS returned during
validation. That closes the window in which a name resolves to a
permitted address during the check and a forbidden one by the time the
connection is made. Every redirect hop goes through the same check, and
redirects are capped at three.

**Dial-time recheck.** The guard's URL check happens twice: once before
the request (`CheckURL`, rejecting a disallowed scheme or a hostname that
resolves nowhere useful), and again inside the actual dial
(`Guard.DialContext`), which re-resolves the hostname and checks the
exact address about to be connected to. This second check is what closes
a DNS-rebinding attack: a name that resolved to a public address when it
was first checked but has since been repointed at a private one is caught
at the moment of connection, not just at submission time. Every redirect
hop is re-checked the same way, capped at three hops.

**No shell execution.** Nothing in the collector or API code path
executes a shell, and no endpoint accepts a command, a path, or a
filesystem location from a caller. (The macOS LaunchAgent installer,
`internal/launchd`, does invoke `launchctl` directly as a fixed local
binary with fixed arguments to install or remove the agent; that call
takes no input from the network or from any API caller, and it does not
go through a shell.)

**Audit log.** Every mutating API request is wrapped so it writes exactly
one row to an append-only audit table (principal, action, target, result,
timestamp), whether the request succeeds or fails.

**Credential redaction.** The structured logger
(`internal/config.NewLogger`) replaces the value of any log attribute
whose key contains `token`, `secret`, `password`, `authorization`,
`apikey`, `api_key` or `credential` with `[redacted]`, so a bearer token
can't end up in a log file, a crash report, or a support bundle by
accident.

**The hub runs as the invoking user.** It is a user-level process, never
installed or run as root.

What this does not yet cover: multi-user access control (there is
currently one caller, authenticated by one shared token) and anything to
do with the macOS app, the widget, or a web dashboard, none of which
exist yet.

## Development

Build:

    go build ./...

Test, always with the race detector, since the scheduler and the
incident state machine are shared across goroutines by design:

    go test -race ./...

Vet:

    go vet ./...

Confirm the Raspberry Pi target still builds. This has to succeed with no
C toolchain involved, because `modernc.org/sqlite` is chosen specifically
so it doesn't need one:

    CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./...

New logic needs a test, in particular anything touching the SSRF guard,
the incident state machine, retention, or the API's auth and validation
paths. See `CONTRIBUTING.md` for more on how the codebase is organized.

## Troubleshooting

**`beacon` says the hub is not responding.** Confirm `beaconhub` is
actually running, and that the CLI and the hub agree on `BEACON_PORT` if
you've set a non-default one. `beacon diagnostics` reports the measured
API latency and the scheduler's last tick if the hub is reachable at all.

**The hub rejected the token / "token is out of sync".** The CLI reads
the token from `<BEACON_DIR>/token`. If you ran the hub once, deleted its
database, and let it regenerate a new token, or if you're pointing the
CLI at a different `BEACON_DIR` than the hub, the token the CLI is
sending won't match what the hub has on disk. Confirm both processes are
using the same `BEACON_DIR`, or just re-read the current token from that
file.

**Where's the database?** `<BEACON_DIR>/beacon.db`, alongside its
`-wal` and `-shm` files while the hub is running (SQLite's WAL mode). On
macOS with no override, that's
`~/Library/Application Support/Beacon/beacon.db`. `beacon diagnostics`
reports its size and row counts per retention tier.

## Contributing

See `CONTRIBUTING.md` for how to build, test and structure a change, and
`CODE_OF_CONDUCT.md` for community expectations. Report a security issue
privately per `SECURITY.md` rather than as a public issue.

## License

MIT. See `LICENSE`.

## Protected branch setup

This is not something the repository contents can enforce; it has to be
applied by a maintainer in GitHub's own settings (Settings → Branches →
branch protection rule for `main`). At minimum:

- Require a pull request before merging, with at least one approval.
- Require status checks to pass before merging, and select the CI jobs
  in `.github/workflows/ci.yml` (gofmt, go vet, test, govulncheck,
  cross-compile) as required checks.
- Require branches to be up to date before merging.
- Do not allow force pushes to `main`.
- Do not allow deletion of `main`.

**Last updated:** 2026-08-29 11:47 PDT

