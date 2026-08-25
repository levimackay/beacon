# Beacon design

Date: 2026-08-24

## What Beacon is

A personal infrastructure monitor: one Mac app that tells you, in two seconds,
whether your machines, services and websites are healthy. The governing
constraint is that the owner should never have to configure a database, a
reverse proxy, a certificate, a port forward or a YAML file to monitor their
own hardware.

## Governing decisions

### One hub binary, not a service mesh

The original brief described `api`, `worker` and `agent` as separate services.
They collapse into a single Go binary, `beaconhub`, which contains the HTTP
API, the SQLite store, the scheduler, the collectors and the incident engine.

A separate slim agent process only earns its existence once there are three or
more monitored machines. Until then the hub monitors the host it runs on
directly. When the Raspberry Pi arrives, the identical binary is
cross-compiled for `linux/arm64` and takes over the hub role; the Mac app
repoints and nothing else changes.

Consequence: no PostgreSQL, no Redis, no nginx, no Docker Compose, no
migrations the user has to run.

### The hub runs on the Mac first, the Pi later

The hub ships first as a macOS user-level launchd agent (`~/Library/LaunchAgents`, no root,
no sudo), installed by Beacon.app on first run.

Accepted limitation while the hub lives on the Mac: monitoring pauses when the
Mac sleeps, so an overnight outage is detected at wake rather than at the time
it happened. This is a property of the deployment, not of the code, and it
disappears when the hub moves to the Pi.

### Tailscale supplies transport, TLS and identity

Exactly two transport modes exist. There is deliberately no third.

- **Local** — the hub binds `127.0.0.1` only. A bearer token is generated on
  first run, stored in the macOS Keychain on the client side and in a `0600`
  file on the hub side.
- **Tailnet** — the hub is published with `tailscale serve`, which supplies a
  real HTTPS certificate, a stable `https://<host>.<tailnet>.ts.net` URL and
  verified caller identity headers. The listener is bound to the Tailscale
  interface.

Binding `0.0.0.0` is not an option the software offers. This is how "no unsafe
fallback networking" is enforced rather than merely documented.

Because the network performs authentication, Beacon has no login screen, no
password hashing, no session store, no CSRF surface and no account recovery
flow. Multi-user support later is a mapping from tailnet identity to role; the
request pipeline already carries an authenticated principal, so this is an
addition rather than a rewrite.

## Components

| Component | Language | Role |
|---|---|---|
| `beaconhub` | Go | API, store, scheduler, collectors, incident engine |
| `Beacon.app` | Swift / SwiftUI | Menu bar panel, main window, notifications, Keychain |
| `BeaconWidget` | Swift / WidgetKit | Small, medium and large widgets |
| `beacon` | Go | CLI against the same API |

`gopsutil` provides host metrics on both `darwin` and `linux`, so there is no
hand-written `/proc` parsing and correspondingly no `/proc` fixture tests. The
ARM64 story is a cross-compile check in CI plus tests over Beacon's own
threshold and incident logic.

SQLite is `modernc.org/sqlite` (pure Go, no cgo), which keeps
`GOOS=linux GOARCH=arm64` cross-compilation free of a C toolchain. Storage sits
behind a `Store` interface so that PostgreSQL, if it is ever genuinely needed,
is a swap rather than a rewrite.

## Data flow

    scheduler tick
      -> collector (host / website / service)
      -> sample written to SQLite
      -> threshold evaluation
      -> incident state machine
      -> incidents table + alert queue
      -> Beacon.app polls GET /v1/snapshot (single ETag'd request)
      -> app writes snapshot to the App Group container
      -> WidgetCenter.reloadTimelines
      -> UNUserNotificationCenter on state change

Widgets do not perform network requests. The app is the only network client
and pushes a compact snapshot into shared storage.

Polling intervals: 15s while the app is frontmost or the menu bar panel is
open, 60s otherwise. Server-sent events are not implemented; they are the
upgrade path if 60s ever feels slow.

## What is monitored in v1

- **Hosts** — CPU, memory, disk usage, load, uptime, temperature where the
  platform exposes it, OS and kernel version, hub version.
- **Websites** — HTTP status against an expected code, response time,
  availability, TLS certificate expiry.
- **Services** — process or unit liveness, via `launchctl` on macOS and
  systemd D-Bus on Linux.

## Incident model

Per target, a state machine over `healthy`, `warning`, `down`, `unknown`.
Entering a non-healthy state opens an incident row; returning to healthy
closes it and records the duration.

### Flap suppression: this was an open question, now resolved

Earlier drafts of this document flagged flap suppression as unresolved: a
target bouncing between up and down would open and close an incident on
every sample, which is a useless incident log on its own and would be a
notification storm once alerting exists. The resolution, implemented in
`internal/incident.Machine`:

A transition is only confirmed after it has been observed for N consecutive
samples, and opening uses a different N than closing.

- **Opening or escalating an incident needs 2 consecutive samples.** This
  absorbs a single bad reading (a dropped packet, a timeout past the
  threshold, a five-second CPU spike) without logging it, while still
  catching a real problem within a couple of check intervals.
- **Closing an incident needs 3 consecutive healthy samples.** Closing is
  deliberately held to a higher bar than opening, because the two guard
  against different failure modes and one shared number cannot serve both.
  A target that is still genuinely broken, a server mid-restart, a site
  failing most requests but serving one lucky 200, often produces exactly
  one good sample in the middle of a real outage. Resolving on that sample,
  only to reopen on the very next bad one, is the same flapping this
  mechanism exists to suppress, just relocated to the recovery boundary
  instead of removed. The cost of the higher bar is a few extra seconds of
  showing "down" after a genuine fix, which is a small price next to a
  resolved/reopened/resolved churn in the incident log, and, once alerting
  ships, the identical churn in notifications.

The one exception: a brand-new target's first-ever confirmation always uses
the opening threshold, even when the destination state is healthy, because
there is no already-open incident at risk of premature closure to protect
against. Without that exception, every freshly added, already-healthy
target would sit at "unknown" for an extra sample for no benefit.

Both thresholds are fields on `Machine`, not command-line flags or config:
the defaults are chosen with reasoning above, and a knob only earns a place
in the operator-facing surface once someone has an actual, specific reason
to move it. A caller with such a reason (an unusually long check interval,
say) can still set the fields directly after construction.

### Network outage detection: "the network is down, not your sites"

A single-machine hub has a specific false-positive mode that per-target flap
suppression alone does not fix: when the Mac running `beaconhub` loses its
own uplink, every website target fails at once, and without anything
smarter than one state machine per target, Beacon opens an incident against
every site, blaming each of them for a problem none of them has.

The hub already runs a mix of checks that need the network (website checks)
and checks that never leave the machine (host metrics via `gopsutil`,
service liveness via `launchctl`/systemd D-Bus). That split is the signal:
if every website target is confirmed down at the same time that a host or
service target is confirmed healthy, the healthy local check proves the
collector process and the machine itself are working, which leaves "this
machine cannot currently reach the internet" as a far better explanation
than "every independently hosted site happened to break at once." The
scheduler (`internal/scheduler/network_outage.go`) checks this on every
confirmed transition and, when it holds, opens one incident against a
synthetic "Network" target instead of one against every site, folding away
any per-site incident that already opened before the full picture was in.

This has to be designed so it never explains away a real outage that
happens to span several sites, which is the actual tradeoff:

- It requires **every** enabled website target to be down, not most of
  them. One target still reachable is itself proof the network path out of
  the machine works, which rules out a network explanation immediately and
  correctly leaves the down targets to raise their own incidents. A real
  outage affecting, say, 4 of 5 configured sites is not a network verdict
  under this rule; it is 4 real incidents, which is what it should be.
- It requires an actually-**confirmed-healthy** local target as the
  control, not merely the absence of a local failure. No host or service
  target configured, or one that is itself down, means there is nothing to
  compare the website failures against, so the function reports no outage
  and every site raises its own incident as usual. The hub seeds a host
  target for itself on first run, so this control is normally always
  present, but the rule does not assume it is.
- It requires **at least two** website targets. With exactly one, "the site
  is down" and "the network is down" produce an identical observation, and
  there is no second witness available to break the tie, so Beacon does not
  guess and raises the site's own incident.

The chosen bias, throughout, is that missing the chance to merge several
incidents into one costs a slightly noisier incident log; treating a real
outage as a network hiccup costs the user's trust in the tool the next time
it says "just your network," which is far more expensive. Every condition
above is written to fail toward raising real incidents, never toward
suppressing them, whenever the evidence is ambiguous.

Once the outage clears, any website target still confirmed down gets its
own incident at that point, backdated to the moment of recovery rather than
to whenever it actually started failing: while the network explanation
held, Beacon had no way to distinguish "down because of the network the
whole time" from "down for its own reasons partway through," and it would
rather under-report an incident's duration than invent a start time it
cannot support.

Alerting rules: one notification on open, one on recovery, nothing in between.
A cooldown suppresses repeat notifications for the same target. `unknown`
(the hub could not reach the target, or the app could not reach the hub) is
displayed as a distinct state and never rendered as healthy.

## Retention

Raw 15s samples are kept 6 hours, rolled into 5-minute buckets kept 7 days,
then 1-hour buckets kept 90 days. The rollup job runs hourly and deletes as it
goes. SQLite runs in WAL mode with periodic incremental vacuum. For a single
host this is on the order of 14,000 raw rows per day, which is not a threat to
the Pi's storage.

## Security

- No shell execution anywhere in the codebase. No endpoint accepts a command,
  a path or a filesystem location.
- Website checks resolve DNS before connecting and reject loopback, private,
  link-local and cloud metadata address ranges. Each redirect hop is
  revalidated, with at most three hops. This is the SSRF control.
- Every mutating request writes an audit row (principal, action, target,
  timestamp, result).
- Rate limiting on the API.
- Typed request and response models with validation at the boundary.
- Secrets never appear in logs; the log formatter redacts token fields.
- The hub runs as the invoking user, never as root.

## Error handling and offline behaviour

When the app cannot reach the hub it shows the last known snapshot with an
explicit staleness banner and the age of the data. It never presents stale
data as current health. The widget does the same. When the hub cannot reach a
target it records `unknown` rather than `down`, and distinguishes the two in
the UI, because "I cannot see it" and "it is broken" are different facts.

The hub buffers alert state across restarts by persisting incident state, so a
restart does not re-notify for an outage the user has already been told about.

## Testing

- Table-driven tests over the threshold evaluator and the incident state
  machine, driven by an injected clock so durations are deterministic.
- `httptest` coverage of the API surface: authentication, authorisation, the
  401/403 matrix, malformed payloads, and rejection of SSRF-range targets.
- A retention test that inserts synthetic samples spanning 30 days, runs the
  rollup and asserts both the resulting row counts and the correctness of
  aggregate queries afterwards.
- Failure simulations: hub killed mid-poll, connection refused, expired token,
  target unreachable, recovery, duplicate-alert suppression.
- Swift unit tests over snapshot decoding and incident formatting; a single
  XCUITest that opens the menu bar panel. No exhaustive UI suite.

## Explicitly out of scope for v1

GitHub integration, Docker monitoring (the owner runs no Docker), the web
dashboard, multi-user accounts, an agent process separate from the hub,
automatic updates, and push notifications. Each is an addition to this
architecture rather than a revision of it.

## Implementation slices

Each slice is independently reviewable and leaves the system working.

1. Repository, protocol types, SQLite store, retention and rollup.
2. `beaconhub` on macOS: host metrics, website checks, incident engine,
   `GET /v1/snapshot`, plus the `beacon` CLI. Milestone: `beacon status`
   returns real data in the terminal.
3. `Beacon.app`: menu bar panel, settings, Keychain, LaunchAgent installation,
   notifications. Milestone: the two-second glance works.
4. `BeaconWidget`: small, medium and large, fed by the App Group snapshot.
5. Main window: Overview, Devices, Websites, Services, Incidents, Diagnostics.
6. Raspberry Pi: `linux/arm64` cross-compile, installer and uninstaller,
   systemd unit, `tailscale serve`, and migration of the hub from Mac to Pi.
7. Dedicated security review and hardening pass.
8. Documentation, CI, and repository furniture.
