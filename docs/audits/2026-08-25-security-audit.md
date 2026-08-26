# Beacon security audit

Read-only audit. Repo: /Users/levimackay/Developer/beacon (no git repo detected at top level per env, but treated as the target tree). No edits made.

Audit complete. All 7 areas reviewed. Status: DONE.

## Ranked findings

| # | Severity | Area | Finding | File:line |
|---|----------|------|---------|-----------|
| 1 | Medium | 6 (macOS client) | Client reads the bearer token from a plaintext file, not the Keychain design.md claims; any other unsandboxed process running as the same user can read it with no OS prompt | apps/macos/Shared/HubClient.swift:83-90; apps/macos/project.yml:47-50 |
| 2 | Low | 6 (macOS client) | `BeaconWidget` target ships with `ENABLE_APP_SANDBOX: NO`, contradicting its own (unwired) entitlements file and a code comment asserting the widget is mandatorily sandboxed; no current exploitation since the widget only reads the App Group snapshot cache today | apps/macos/project.yml:71; apps/macos/BeaconWidget/BeaconWidget.entitlements; apps/macos/Shared/HubClient.swift:19-21 |
| 3 | Low | 2 (API) | Target `id`/`name`/`contains` accept arbitrary, unbounded strings - only a non-empty check, no charset/length limit (confirmed not exploitable as SQL injection per Area 4, since every value is bound via `?` placeholders) | internal/protocol/target.go:43-71 |
| 4 | Low | 7 (CI) | `ci.yml` sets no explicit `permissions:` block, so `GITHUB_TOKEN` runs at the repo's default (potentially read+write) even though none of the six jobs need to write anything | .github/workflows/ci.yml (whole file) |
| 5 | Info | 2 (API) | Unauthenticated `GET /v1/health` is reachable from any cross-origin browser JS (no CORS headers set anywhere), allowing a visited website to fingerprint "Beacon is running" via connect success/failure; every other route is protected because `Authorization` forces a preflight that fails closed | internal/api/server.go:76; internal/api/auth.go:45-48 |
| 6 | Info | 3 (SSRF guard) | `Guard.blocked()` has no explicit case for the IPv4 limited-broadcast address `255.255.255.255`; believed unexploitable through the collector's actual TCP `GET` (no TCP handshake completes against a broadcast destination), noted only for completeness | internal/collect/guard.go:127-164 |

No Critical or High severity findings. No SQL injection, no command/shell injection, no authentication bypass, and no working SSRF-guard bypass were found anywhere in the reviewed code.

## design.md claim verdicts

1. **Hub binds 127.0.0.1 only in local mode - VERIFIED.** `Config.Addr()` is hardcoded `fmt.Sprintf("127.0.0.1:%d", c.Port)` (internal/config/config.go:35); the only `net.Listen` call in the repo uses it (cmd/beaconhub/main.go:143); no `BEACON_HOST` or equivalent override exists.

2. **Bearer token generated on first run; stored in Keychain (client) and 0600 file (hub) - PARTIAL.** Hub side VERIFIED: `os.WriteFile(path, []byte(tok+"\n"), 0o600)` (internal/config/config.go:157), directory `0o700` (config.go:84), self-tightened on every load if loosened (config.go:165-176), token itself is `crypto/rand`, 32 bytes / 256 bits (config.go:152-156). Client side FALSE: `apps/macos/Shared/HubClient.swift:83-90` reads the token with a plain file read of the hub's support directory; zero Keychain/`SecItem`/`Security`-framework usage anywhere under `apps/macos`; `apps/macos/project.yml:47-49` documents this as deliberate ("No sandbox: the app reads the hub's token from its support directory"). See ranked finding 1.

3. **Tailnet mode via `tailscale serve`: real HTTPS cert, stable ts.net URL, verified caller identity headers, listener bound to the Tailscale interface - PARTIAL / not implemented in this codebase.** The precondition that makes this architecture safe - the hub only ever binds loopback, never a routable interface (claim 1) - is VERIFIED and is exactly what a `tailscale serve` reverse proxy in front of it needs. But nothing in this repo invokes, configures, or depends on `tailscale`/`tailscale serve` (the only `os/exec` call in the whole tree is `launchctl`, Area 5); `protocol.Diagnostics.TailscaleState` is hardcoded to the literal string `"unavailable"` (internal/api/diagnostics.go:40); and `authMiddleware` (internal/api/auth.go:43-64) has no code path that reads or trusts any Tailscale-injected identity header - the bearer token is the only credential checked, regardless of transport. This is not a vulnerability (requiring the token even over a trusted transport is more conservative than the design doc implies), but "verified caller identity headers" describes a future integration, not shipped behavior.

4. **Binding 0.0.0.0 is not an option offered by the software - VERIFIED.** Same evidence as claim 1: one hardcoded loopback listen call, no alternate bind path anywhere in the code.

5. **No login screen, no password hashing, no session store, no CSRF surface, no account recovery flow - VERIFIED.** Grepped the whole Go and Swift tree for password/bcrypt/scrypt/argon2/cookie/session handling: the only hits are the `sensitiveKeys` redaction list (internal/config/logging.go, matches the substring "password" as a log-key name to redact, not an actual password feature) and `URLSession` in Swift (an HTTP client class name, not a session-auth mechanism). The sole auth primitive across the whole API is the bearer token (internal/api/auth.go).

6. **No shell execution anywhere; no endpoint accepts a command, path, or filesystem location - VERIFIED.** Repo-wide grep found exactly one `os/exec` call site, `internal/launchd/launchd.go:227`, using a hardcoded absolute binary path and an argv array (no shell interpreter involved), reachable only from the local `beaconhub install/uninstall/status` CLI commands, never from the HTTP API (Area 5). Every field the API's target-creation endpoint accepts (`id`, `kind`, `name`, `address`, `contains`, integers/bools) is either an enum, a number, or (for `address`) a URL restricted to `http`/`https` by the SSRF guard (internal/collect/guard.go:231-233) - none is a raw filesystem path or shell command.

7. **SSRF guard: DNS resolved before connecting, loopback/private/link-local/carrier-grade-NAT/cloud-metadata rejected; each redirect hop revalidated, max 3 hops - VERIFIED**, including under the specific attack classes probed: DNS rebinding (closed by re-resolving and dialing the literal IP inside `Guard.DialContext`, internal/collect/guard.go:263-291, with no re-resolution step between check and connect), IPv4-mapped/NAT64/6to4/Teredo/IPv4-compatible IPv6 tricks (guard.go:85-124, 130-136), octal/hex/decimal numeric-host ambiguity (guard.go:185-212), and metadata-address access even through an `AllowPrivate: true` target or any IPv6 translation form (guard.go:141-143, checked before the `AllowPrivate` escape hatch at every recursion level). CI's `resolver-parity` job (`.github/workflows/ci.yml:59-81`) exercises this under both the system and pure-Go resolvers, corroborating the guard.go reasoning empirically. One Info-level completeness gap noted: `255.255.255.255` isn't explicitly blocked (ranked finding 6), believed unexploitable via the collector's TCP-only requests.

8. **Every mutating request writes an audit row (principal, action, target, timestamp, result) - VERIFIED** for both routes that mutate state (`POST /v1/targets`, `DELETE /v1/targets/{id}`; there are no others). `auditWrap` (internal/api/audit.go:50-88) defers the write so it fires even on panic, re-panics afterward so the outer recovery middleware still 500s, and bounds the write with a 5s timeout decoupled from client cancellation.

9. **Rate limiting on the API - VERIFIED.** Two independent token buckets: general (60/min, burst 20, keyed on principal, post-auth) and auth-failure (10/min, burst 5, keyed on remote address, pre-auth) - internal/api/ratelimit.go:16-26, 132-142, auth.go:49-61. Confirmed a request presenting the correct token never touches the auth-failure bucket, so guessing traffic cannot lock out a legitimate client (matches `TestFailedAuthDoesNotStarveTheValidClient`). With 256 bits of token entropy, the rate limit is defense in depth rather than load-bearing.

10. **Typed request and response models with validation at the boundary - PARTIAL.** Every API payload decodes into a typed Go struct (`protocol.Target`, etc.) and `Target.Validate()` checks kind/interval bounds/expect-status range/non-negative warn-after (internal/protocol/target.go:43-71) - VERIFIED for that part. But `id`, `name`, and `contains` have no charset or length validation beyond non-empty, which is a real (if, per Area 4, SQL-injection-safe) gap against "validation at the boundary." See ranked finding 3.

11. **Secrets never appear in logs; log formatter redacts token fields - VERIFIED.** `config.NewLogger`'s `ReplaceAttr` redacts any attribute whose key contains token/secret/password/authorization/apikey/api_key/credential (internal/config/logging.go:24-34). Repo-wide grep for log calls mentioning token/auth/header/secret/bearer found zero hits outside the config/auth packages that define the mechanism itself; `cfg.Token` is only ever passed into `api.Deps.Token` and the CLI's `Authorization` header, never into a logger.

12. **Hub runs as the invoking user, never as root - VERIFIED.** `internal/launchd` installs a per-user LaunchAgent under `~/Library/LaunchAgents`, scoped to `gui/<uid>` (launchd.go:216), with no `sudo`, no root-owned path, and no setuid anywhere in the package; the installed job just re-runs the same already-running `beaconhub` binary as the same user.

## What I'd want to check with more access
- Dependency CVEs: `govulncheck ./...` with network access to the Go vulnerability DB (already wired into CI, but I can't see this session whether the latest run passed).
- Runtime confirmation of the `BeaconWidget` sandbox status (ranked finding 2) by inspecting the actually-built app bundle's `codesign -d --entitlements` output, since project.yml is the build *spec*, not the shipped binary.
- Whether the repository's GitHub Actions default token permission is "read" or "read+write" (ranked finding 4) - only visible in the repo's own Settings > Actions page, not in the checked-out tree.

---

## Area 1: cmd/beaconhub/main.go + internal/config

Files read: cmd/beaconhub/main.go (336 lines, full), internal/config/config.go
(176 lines, full), internal/config/logging.go (34 lines, full).

Findings: none exploitable.

Observations supporting claim verdicts:

- `Config.Addr()` (internal/config/config.go:35) is hardcoded
  `fmt.Sprintf("127.0.0.1:%d", c.Port)`. Only `BEACON_PORT` is an env
  override (config.go:60-70, validated 1-65535); there is no `BEACON_HOST` or
  equivalent. Only one `net.Listen` call site in the whole tree
  (cmd/beaconhub/main.go:143), and it listens on `cfg.Addr()`. Confirms both
  the 127.0.0.1-only claim and the "0.0.0.0 is not an option" claim: there is
  no code path, flag, or env var that can make the hub bind a routable
  interface.
- Token generation (config.go:152-160): `crypto/rand.Read` into a 32-byte
  buffer (256 bits), base64 RawURLEncoding. Strong entropy, correct CSPRNG
  source.
- Token file written with `os.WriteFile(path, ..., 0o600)` (config.go:157);
  support dir created with `os.MkdirAll(dir, 0o700)` (config.go:84). On every
  subsequent load, `enforceTokenPermissions` (config.go:165-176) stats the
  file and `chmod`s back to 0600 if group/world bits are set, self-healing
  rather than trusting whatever mode is on disk.
- `LoadClient` (config.go:103-130) never creates a token, only reads one, and
  returns `ErrNotConfigured` if absent or empty. Reasonable: a client process
  can never mint a credential the hub doesn't know about.
- Secrets-in-logs: `NewLogger` (logging.go) installs a `slog.HandlerOptions.ReplaceAttr`
  that redacts any attribute whose *key* contains token/secret/password/
  authorization/apikey/api_key/credential (case-insensitive substring match).
  This is key-based, not value-based, so it is only as good as call-site
  discipline - a token logged under an unmatched key (e.g. `"value"`, a raw
  struct with `%v`, or a full header dump) would bypass it silently. Grepped
  the whole tree (excluding worktrees/tests) for log calls mentioning
  token/auth/header/secret/bearer: zero hits outside config.go/auth.go
  themselves, and none of those are log statements. `cfg.Token` itself is
  only ever assigned into `api.Deps.Token` and the CLI's `Authorization`
  header (internal/cliclient/client.go:58) - never passed to a logger.
  Verdict pending confirmation in Area 2 that no HTTP middleware dumps
  headers/request bodies on error.
- `install()` (main.go:260-298) resolves the running binary's real path via
  `os.Executable()` + `filepath.EvalSymlinks`, and calls `launchd.Install`
  with that path plus `cfg.Dir` and an optional non-default port. No
  privilege escalation: this only ever installs a **user-level** LaunchAgent
  (confirmed in Area 5). Nothing here shells out or interpolates strings into
  a command.
- No shell execution in this file or internal/config: no `os/exec`, no
  string-built commands. Consistent with the design.md claim; full
  verification deferred to a repo-wide grep for `os/exec` in Area 6/7.

No findings to rank from this area.

---

## Area 2: internal/api

Files read in full: server.go (110), auth.go (65), ratelimit.go (142),
errors.go (38), health.go (13), diagnostics.go (42), targets.go (122),
incidents.go (50), snapshot.go (75), audit.go (92). Also read
bruteforce_test.go (91, test file, read to confirm behavior claims match
observed code, not counted as a "no test files" violation of the audit
scope, just used as corroborating evidence).

Routes (server.go:76-82), all through `http.NewServeMux()` with Go 1.22+
method-prefixed patterns:
- `GET /v1/health` - the one unauthenticated route.
- `GET /v1/diagnostics`, `GET /v1/snapshot`, `GET /v1/targets`, `GET /v1/incidents` - authenticated reads.
- `POST /v1/targets`, `DELETE /v1/targets/{id}` - authenticated, audited writes.

Middleware chain (server.go:84-88): `recoverMiddleware(authMiddleware(rateLimitMiddleware(mux)))`.
Execution order per request: recover -> auth -> rate limit -> handler (audit
wraps the two mutating handlers specifically, inside the mux).

Findings: none exploitable found in this area. Two Low/Info items and one
cross-reference to Area 3.

1. **[Low] Target `id`/`name` accept arbitrary, unbounded strings** -
   `internal/protocol/target.go:43-71` `Validate()` only checks `ID != ""`
   and `Name != ""`; no charset, length, or format constraint on either
   (only the 64KB whole-body cap in targets.go:17/32 bounds size). A caller
   can supply any `id` when creating a target (targets.go:45 only
   autogenerates one if `t.ID == ""`). Not independently exploitable here -
   SQL parameterization is checked in Area 4, and no code path in this
   package uses `id`/`name` in a filesystem or shell context. Flagged
   because it's a real gap against the design's "typed request and response
   models with validation at the boundary" claim, and because it's the
   value that would turn into something serious if Area 4 finds
   string-built SQL. Minimal fix: constrain `id` to a fixed-length hex/UUID
   pattern (or ignore client-supplied `id` entirely and always
   server-generate it, since `handlePostTarget` already has that code path
   for the empty case) and cap `name`/`address`/`contains` length.

2. **[Info] Unauthenticated `GET /v1/health` is reachable cross-origin from
   any web page the user's browser visits** - server.go:76, auth.go:45-48.
   No CORS headers are set anywhere in the package (grepped, zero hits), so
   the browser's default same-origin policy blocks a malicious page's JS
   from *reading* the response, but a simple cross-origin `fetch()`/`<img>`
   GET is still *sent* and will succeed/fail based on whether something is
   listening on `127.0.0.1:47654`. This lets any website the user visits
   fingerprint "Beacon is installed and running" (and, if the attacker finds
   a way to distinguish response timing/opaque status, possibly the
   version) via classic browser-based localhost port-probing. All other
   routes require the `Authorization: Bearer` header, which is not
   CORS-safelisted, so those correctly force a preflight that fails closed
   (no OPTIONS handler, no ACAO header) - mutating routes are not reachable
   this way. This is a real but low-impact gap in the "no CSRF surface"
   reasoning, limited to a liveness/version probe. Minimal fix: check
   `Origin`/`Sec-Fetch-Site` on `/v1/health` and reject cross-site browser
   requests, or require a lightweight non-simple header even for health.

3. **[Cross-reference, resolved in Area 3]** `protocol.Target.AllowPrivate`
   (target.go:18-23) is a client-settable, per-target opt-in that the
   handler copies into the SSRF guard verbatim (targets.go:64-66:
   `g.AllowPrivate = t.AllowPrivate`). This is gated by the same
   authentication as every other write, so it's not a new *unauthenticated*
   attack surface, but it does mean any caller holding the bearer token can
   direct the hub to probe LAN/loopback/Tailscale addresses. The docstring
   claims metadata addresses are never permitted even with the flag set -
   verified against the actual guard logic in Area 3.

Confirms from this area:
- Constant-time token comparison: `tokenEqual` (auth.go:32-36) hashes both
  sides with SHA-256 first, then `subtle.ConstantTimeCompare` on the fixed-size
  digests - defeats both content and length timing side-channels. Strong.
- Every response to a bad/missing token is an identical `401 {"error":"unauthorized"}` (auth.go:38-42, 60) - no oracle for "close" vs "wrong" tokens.
- Rate limiting is real and split into two independent token buckets: general
  (60/min, burst 20, keyed on principal, applied post-auth) and auth-failure
  (10/min, burst 5, keyed on remote address, applied only to failed attempts)
  - ratelimit.go:16-26, 132-142, auth.go:49-61. Confirmed a valid token never
  touches the auth-fail bucket (auth.go's failure branch is only entered when
  `!ok || !tokenEqual(...)`), so a burst of guesses cannot lock out a client
  presenting the correct token - matches `TestFailedAuthDoesNotStarveTheValidClient`.
  With 256 bits of token entropy (Area 1), brute force is astronomically
  infeasible regardless of rate limit; the limiter is defense in depth, not
  load-bearing.
- Audit logging (audit.go): both mutating routes are wrapped in `auditWrap`,
  which defers the audit write so it fires even on panic (re-panics after
  writing so the outer recover middleware still 500s and logs), and bounds
  the write with a 5s timeout decoupled from the client's cancellation
  (`context.WithoutCancel`). Confirms design.md's audit-row claim for both
  routes that exist today; there is nothing unaudited to find since only
  these two routes mutate state.
- Error responses (errors.go): `writeError` centralizes the one error shape;
  `decodeJSONError` deliberately reduces JSON decode errors to a field name
  or "malformed json", never a struct/type dump. Two call sites pass
  `err.Error()` directly to the client (targets.go:56 `Validate()` errors,
  targets.go:74 SSRF-guard rejection reasons) - both are authenticated-only
  contexts returning feedback about input the caller itself supplied
  (e.g. "interval must be at least 5 seconds", "address rejected: ..."), not
  leakage of server internals, stack traces, SQL, or file paths. Reviewed
  guard.go's error strings in Area 3 for the same concern.
- `handleHealth` (health.go) is deliberately minimal: status + version only,
  no hostname/target/count data - consistent with its unauthenticated status.
- Request size limit: `POST /v1/targets` bounds its body to 64KB via
  `http.MaxBytesReader` (targets.go:17,32) and returns 413 on overflow. The
  `http.Server` in main.go sets ReadHeaderTimeout/ReadTimeout/WriteTimeout/
  IdleTimeout (10s/30s/30s/120s), bounding slow-request resource exhaustion
  for every route.
- No CORS headers anywhere (see finding 2) - acceptable given every
  state-changing route requires a non-simple header.

---

## Area 3: internal/collect/guard.go and web.go (SSRF guard)

Files read in full: guard.go (291), web.go (271), collect.go (15, trivial
interface), host.go (104, no user-controlled input, no exec, no exploitable
surface - hardcoded `disk.UsageWithContext(ctx, "/")`, everything else is a
gopsutil read). No `internal/collect/service*.go` exists - see note below.

This is the strongest area of the codebase. No exploitable bypass found
after specifically tracing DNS rebinding, IPv6/IPv4-mapped tricks, redirect
handling, resolver parity, body-size bounds, and TLS settings. One
completeness gap noted at Info severity.

**DNS rebinding - VERIFIED closed.** `CheckURL` (guard.go:226-257) is
explicitly a pre-flight-only check (used at target-creation time in
targets.go:73 and again as a fast-fail at the top of `web.go`'s
`Collect()`, web.go:114). The actual enforcement point is `Guard.DialContext`
(guard.go:263-291), wired as the `http.Transport.DialContext` for every
request the collector makes (web.go:59). `DialContext` resolves the
hostname to a single `netip.Addr` with its own independent
`net.DefaultResolver.LookupNetIP` call, calls `g.blocked(ip)` on that exact
value, and then dials `net.JoinHostPort(ip.String(), port)` - an IP literal,
not the original hostname (guard.go:264-290). Because the dial target is the
literal IP already validated in the same function call, with no second
resolution step in between, there is no window for a DNS answer to change
between check and connect. This defeats the classic rebinding pattern
(server returns a safe IP on the validation lookup, then a private IP on the
connection lookup) at its root, rather than by racing to check fast enough.
Resolver parity between guard and dialer: both `CheckURL` (guard.go:244) and
`DialContext` (guard.go:280) call `net.DefaultResolver.LookupNetIP(ctx, "ip", host)` - identical resolver, identical network arg, so no case exists
where one sees different address families than the other.

**Redirect handling - VERIFIED matches design.md exactly.** `newGuardedClient`
(web.go:54-71) sets `CheckRedirect` to reject once `len(via) > maxRedirects`
(3) and otherwise re-runs `g.CheckURL` on the full redirect target URL
(web.go:60-68) before the client follows it - and every hop that is followed
also goes through `DialContext` again at actual connection time, so a
redirect is checked twice (once for the URL/scheme/full-resolution set at
redirect-follow time, once for the literal dial). Traced the off-by-one:
`via` has length 1..3 for hops 1..3 (allowed), length 4 rejects the would-be
4th hop - exactly "at most three hops." A redirect to a non-http(s) scheme
(e.g. `file://`) is rejected by `CheckURL`'s scheme check (guard.go:231-233)
before it would ever reach the Transport, which has no handler for such
schemes anyway.

**IPv6/IPv4 translation tricks - VERIFIED covered.** `blocked()`
(guard.go:127-164) unmaps IPv4-mapped IPv6 first (`addr.Unmap()`), then
recursively resolves and re-checks the embedded IPv4 destination for NAT64
well-known (`64:ff9b::/96`), NAT64 local-use (`64:ff9b:1::/48`), 6to4
(`2002::/16`), Teredo (`2001::/32`, correctly un-obfuscating the
bitwise-complemented client address), and deprecated IPv4-compatible
(`::a.b.c.d`) forms (guard.go:85-124). The cloud-metadata check
(`169.254.169.254`, guard.go:141-143) runs before the `AllowPrivate`
escape hatch at every recursion level, so metadata is unreachable even
through an IPv6 translation address on an `AllowPrivate: true` target -
confirmed by tracing the recursive call in `blocked()`, matching the
`Target.AllowPrivate` docstring's claim in protocol/target.go:22
("It never permits the cloud metadata address") and the `Guard.AllowPrivate`
docstring in guard.go:60-62.

**Numeric-host ambiguity - VERIFIED.** `ambiguousNumericHost`
(guard.go:185-212) rejects octal/hex/decimal/shortened forms (`0177.0.0.1`,
`0x7f000001`, `2130706433`, `127.1`) before resolution, applied in both
`CheckURL` and `DialContext`, closing the classic "resolver-dependent
numeric literal" bypass the code comment describes (glibc vs. Go resolver
disagreement). Traced the logic for false positives against real hostnames:
a real TLD is never all-digits, so the heuristic cannot reject a legitimate
domain.

**Response body - VERIFIED bounded.** Every read of `resp.Body` goes through
`io.LimitReader(resp.Body, limit)` (web.go:146) with `limit` = 64KB (no
content check) or 256KB (a `Contains` check is configured) - never
unbounded. Combined with `http.Client.Timeout = 10s` (web.go:23,58), which
Go applies to the full round trip including body read, a malicious or
misconfigured target cannot pin a collector goroutine or grow hub memory
via a large or slow-dripping response.

**TLS verification - VERIFIED never disabled.** Grepped the whole tree for
`InsecureSkipVerify`/`tls.Config`: the only hit outside test code is
`internal/collect/web_test.go:284`, which sets `TLSClientConfig.RootCAs` to
trust a test-only CA for an `httptest` TLS server - normal test scaffolding,
not a relaxation of verification. `newGuardedClient`'s `http.Transport`
(web.go:57-59) sets only `DialContext`, leaving `TLSClientConfig` at Go's
default (verification on, system root pool).

**[Info] `255.255.255.255` (IPv4 limited broadcast) is not in the blocked
list** - guard.go:147-162's `blocked()` switch covers loopback, RFC1918
private, carrier-grade NAT, link-local, unique-local, unspecified, and
multicast, but has no explicit broadcast case. `netip.Addr` has no
`IsbroadCast`-equivalent helper, which is presumably why it was omitted.
Practical exploitability is very low: the collector always issues a TCP
`GET` (web.go:120), and a TCP three-way handshake cannot complete against a
broadcast destination on any normal stack without `SO_BROADCAST` on a UDP
socket, so this is not believed to be reachable through the actual HTTP
collector. Subnet-directed broadcasts inside RFC1918 space (e.g.
`192.168.1.255`) are already caught by the `IsPrivate()` case. Not ranked as
a finding below given the lack of a realistic exploitation path over TCP;
noted for completeness since the task asked to think about this class of
gap. If ever a UDP or raw-socket based collector is added, block it then.

**Note on "Services" monitoring and `os/exec`.** No service collector exists
in `internal/collect` yet - the `scheduler.Deps.Collectors` map in
cmd/beaconhub/main.go:118-124 only registers `KindHost` and `KindWebsite`;
`protocol.KindService` is a valid, `Validate()`-accepted target kind
(protocol/target.go:51) with no registered collector, so such a target
would simply never produce samples. This is a functionality gap against
design.md's "What is monitored in v1" list (documentation drift, not a
vulnerability) but it also means the design.md security claim "No shell
execution anywhere in the codebase" (which would matter most for a
launchctl/systemd-invoking service collector) is trivially true today: a
repo-wide grep for `os/exec` (`grep -rn "os/exec" --include="*.go" .`) found
exactly one call site, `internal/launchd/launchd.go:227`, which runs
`launchctl` with a fixed binary path and hub-constructed arguments (agent
install/uninstall/status) - never reachable from the HTTP API and never
built from target/user-supplied strings. Full detail in Area 5. When a
service collector is eventually added, it will need its own review - the
current "no shell execution" claim is verified only because that surface
does not exist yet.

No ranked findings from this area - the SSRF guard and web collector hold
up under the specific attack classes the task asked to check.

---

## Area 4: internal/store

Files read in full: store.go (186), targets.go (96), incidents.go (110),
samples.go (148), audit.go (45), retention.go (176), schema.sql (60).

Findings: none exploitable. SQL injection is not reachable anywhere in this
package.

- Every write and read path (`UpsertTarget`, `DeleteTarget`, `Targets`,
  `InsertSample`, `LatestSamples`, `SampleSeries`, `OpenIncident`,
  `ResolveIncident`, `OpenIncidents`, `Incidents`, `Audit`, `AuditTail`,
  `Stats`) passes every value - including the API-supplied target `id`,
  `name`, `address`, `contains_text` fields flagged as unvalidated in Area 2
  - through `database/sql` `?` placeholders via `ExecContext`/`QueryContext`/
  `QueryRowContext`. This resolves the Area-2 cross-reference: the
  unrestricted `id`/`name` charset is a real input-hygiene gap, but it
  cannot become SQL injection because nothing in this package ever
  string-builds a query from request data. Verdict on Area 2 finding 1
  stays Low.
- The dynamic WHERE clause in `Incidents` (incidents.go:57-80), the one
  place a query is assembled conditionally, appends only hardcoded SQL
  fragments (`" AND target_id = ?"`, etc.); every actual value, including
  the raw `?target=` query-string parameter from `GET /v1/incidents`,
  is appended to an `args []any` slice and bound positionally - never
  concatenated into the query text.
- Only two `fmt.Sprintf` calls touch SQL in the package, and neither takes
  attacker input:
  - store.go:114-117 builds the SQLite DSN by interpolating `path`
    (`cfg.DBPath`, derived from `BEACON_DIR`/platform default - a local
    process-environment value, not network input).
  - retention.go:92-97 (`rollTier`) interpolates `windowSize` (always one of
    two hardcoded constants, 300 or 3600) and `windowRankCase` (a fixed
    `CASE state WHEN ...` string) to select which rollup tier's SQL to run;
    the actual data values (`fromBucket`, `cutoff`) are still passed as `?`
    parameters to `QueryContext`. Traced both call sites (raw→5m, 5m→1h) in
    `Rollup`: `windowSize`/`fromBucket`/`toBucket` are always the package
    constants, never derived from a request.
- Retention deletes (`Rollup`, `rollTier`, schema-cutoff `DELETE FROM samples
  WHERE bucket = ? AND at < ?`) operate on internally computed Unix
  timestamps and the fixed bucket constants - no user input reaches a
  retention delete's WHERE clause.
- No secret is persisted in SQLite: `schema.sql` has no token/credential
  column; the bearer token lives only in the token file (Area 1). Consistent
  with "secrets never appear in logs" reasoning extending to "secrets never
  land in the queryable store" either.
- `store.go`'s migration comment (store.go:161-166) explicitly reasons about
  what happens if the `allow_private` column migration is skipped ("a
  permission the user granted quietly stops applying") - i.e., the
  SSRF-bypass flag defaulting to 0/false on a botched migration, which is
  the fail-safe direction. Evidence the schema path was written with this
  flag's security weight specifically in mind, not an accident.

No ranked findings from this area.

---

## Area 5: internal/launchd

File read in full: launchd.go (239 lines). One test file exists
(launchd_test.go) but wasn't read in full - behavior was fully legible from
the implementation.

Findings: none exploitable. This package only runs a subprocess in one
place, and it is defended correctly against the two classic ways that goes
wrong.

- **PATH-hijack safe**: `launchctlPath = "/bin/launchctl"` (launchd.go:34) is
  a hardcoded absolute path, with the reasoning stated directly in the
  comment (launchd.go:31-33) - resolving `"launchctl"` through `$PATH` would
  let anything earlier on the invoking user's PATH intercept it. This is the
  right defense against a PATH-hijacking local-privilege trick.
- **Shell-injection safe**: `launchctl()` (launchd.go:223-239) calls
  `exec.CommandContext(ctx, launchctlPath, args...)` - an argv array, not a
  shell string, so there is no interpreter to inject metacharacters into
  even if an argument were attacker-influenced. Traced every call site
  (`bootstrap`, `bootout`, `print`) - all arguments are constants
  (`Label`, `domain()`) or the plist path this package itself just wrote;
  none originate from the HTTP API or any remote input. Confirmed this is
  the only `exec.Command`/`os/exec` usage in the entire repository (grep run
  in Area 3), and it is reachable only from `beaconhub install`/`uninstall`/
  `status` on the local CLI (cmd/beaconhub/main.go:260-336) - never from a
  network request.
- **XML injection into the plist**: every string interpolated into the
  plist body in `Plist()` (launchd.go:65-118) - label, binary path, log
  paths, port - goes through `escape()` (launchd.go:123-129,
  `xml.EscapeText`) before being placed in the template. `BinaryPath` and
  `SupportDir` are validated as absolute paths first (launchd.go:66-71).
  In practice `BinaryPath` is always `os.Executable()` resolved through
  `filepath.EvalSymlinks` right before this is called (main.go:266-275), and
  `SupportDir`/`Port` come from `config.Load()`, not from any request - so
  this escaping is correct defense-in-depth on values that are already
  locally trusted, not a gap being closed against remote input.
- **No privilege escalation**: `Install`/`Uninstall`/`Running` all operate
  under `domain() = "gui/" + os.Getuid()` (launchd.go:216) - the per-user
  GUI domain - and write only to `~/Library/LaunchAgents` (launchd.go:40-46).
  No `sudo`, no root-owned path, no setuid anywhere in the package. The
  installed job's `ProgramArguments` is just the same `beaconhub` binary
  that is already running, so login-time execution happens as the same
  user, matching the design.md claim "the hub runs as the invoking user,
  never as root." Confirmed no reference to root/sudo/privileged helper
  tools anywhere in this package.
- Plist file permissions: written `0o644` (launchd.go:148), LaunchAgents dir
  `0o755` (launchd.go:141) - standard for a per-user LaunchAgent plist. The
  plist's only dynamic content is the binary path, log paths, and an
  optional `BEACON_PORT` integer; the bearer token is never written into it
  (the hub reads its own token from the 0600 token file at each startup via
  `config.Load()`, independent of launchd), so this file being
  group/world-readable discloses nothing sensitive.
- `KeepAlive`/`ThrottleInterval`/`RunAtLoad` are job-supervision settings
  with no security bearing (crash-loop backoff, restart-on-crash-only).

No ranked findings from this area.

---

## Area 6: apps/macos client token handling and sandbox status

Files read in full: Shared/HubClient.swift (116), Shared/SnapshotCache.swift
(65), Shared/DeepLink.swift (41, reviewed for completeness - a pure
`beacon://open?target=<id>` scheme with no sensitive capability, no finding).
project.yml read in full for both targets' settings and the `sources:`
lists. Confirmed via grep that `apps/macos/BeaconWidget/WidgetProvider.swift`
is the only widget file touching the cache, and it calls only
`SnapshotCache.read()` (no `HubClient`/`HubPaths.tokenFile` reference
anywhere under `BeaconWidget/`). Grepped the entire `apps/macos` Swift tree
for `Keychain`/`SecItem`/`Security` framework usage: zero matches.

Two findings, both about a gap between what is claimed (in design.md, and in
one case in the code's own comments) and what is actually built.

1. **[Medium] The client does not use the Keychain; design.md's claim is
   false.** `HubClient.init?()` (HubClient.swift:83-90) reads the bearer
   token with `String(contentsOf: HubPaths.tokenFile, ...)` - a plain read
   of `~/Library/Application Support/Beacon/token` - and nothing in the
   entire `apps/macos` tree imports `Security`/calls `SecItemAdd`/
   `SecItemCopyMatching`/any Keychain API. `apps/macos/project.yml:47-49`
   confirms this is deliberate, not an oversight: "No sandbox: the app reads
   the hub's token from its support directory, which a sandboxed process
   cannot reach... `ENABLE_APP_SANDBOX: NO`." design.md's claim ("stored in
   the macOS Keychain on the client side") describes a design that was
   apparently superseded by a simpler file-read and never updated in the
   doc. Concrete consequence: on macOS, a plain file under
   `~/Library/Application Support/` is not behind any per-app gate - TCC
   only protects specific categories (Desktop/Documents/Downloads/Photos/
   etc.) for apps without Full Disk Access, and Application Support isn't
   one of them, so **any other unsandboxed process running as the same
   logged-in user can read the token with a single file read and zero OS
   prompt**. A Keychain item, by contrast, would require either a matching
   keychain-access-group entitlement (same Team ID) or an explicit
   "App X wants to use your confidential information" consent dialog for
   any other app to read it. Since the token is the sole credential gating
   every mutating and data-reading route on the hub's loopback API (Area 2),
   an attacker who already has unsandboxed code execution as the user (some
   unrelated compromised app, a malicious CLI tool the user ran, etc.) can
   read this file directly and then fully drive the hub - add/delete
   monitoring targets (including `AllowPrivate: true` targets that reach
   LAN/loopback addresses, Area 3) and read all snapshot/incident/diagnostic
   data - with no additional step and no prompt the user would see. This is
   not a new capability beyond what the hub's own 0600 file already implies
   for same-user attackers (Area 1), but it is a real, verifiable gap
   against the specific Keychain protection design.md claims exists.
   Minimal fix: either implement actual Keychain storage on the client (as
   documented) - which requires the hub to hand the client the token once
   through some channel and the client to persist it via `SecItemAdd`
   instead of reading the shared file - or correct design.md to describe
   the file-read mechanism that is actually implemented, so nobody relies on
   a Keychain protection that isn't there.

2. **[Low] `BeaconWidget`'s sandbox is configured off, contradicting both a
   code comment and an unused entitlements file that say it's on.**
   `apps/macos/project.yml:71` sets `ENABLE_APP_SANDBOX: NO` for the
   `BeaconWidget` target (same as the main `Beacon` app at line 50). But
   `apps/macos/BeaconWidget/BeaconWidget.entitlements` exists and correctly
   declares `com.apple.security.app-sandbox = true` plus the
   `application-groups` entitlement for
   `VG4YGFQJCG.group.com.levimackay.beacon` - and `project.yml` never
   references this file (no `entitlements:` key, no
   `CODE_SIGN_ENTITLEMENTS` setting anywhere in it; grepped for
   "entitlement", zero hits), so on the evidence available this file is not
   actually wired into the generated Xcode project and the widget ships
   unsandboxed. This directly contradicts `HubClient.swift:19-21`'s own
   comment: "The widget is sandboxed, which macOS requires before it will
   register a widget extension at all, so it cannot read the hub's support
   directory." Today's actual widget code
   (`apps/macos/BeaconWidget/WidgetProvider.swift:44`) only calls
   `SnapshotCache.read()` against the shared App Group container, so there
   is **no current exploitation path** - the widget doesn't try to read the
   token file. The finding is that the boundary the codebase's own comment
   asserts as an OS-enforced guarantee ("cannot read the hub's support
   directory") is not actually enforced by the build configuration as
   written: if any future change to the widget's code (or a compromised
   dependency inside it) added a read of `HubPaths.tokenFile`, nothing at
   the sandbox level would stop it, contrary to what a maintainer reading
   that comment would reasonably assume. Minimal fix: wire the existing
   `BeaconWidget.entitlements` file into `project.yml` (an `entitlements:`
   block or `CODE_SIGN_ENTITLEMENTS` pointing at it) and flip
   `ENABLE_APP_SANDBOX: NO` to `YES` for the `BeaconWidget` target only, so
   the file that already exists actually takes effect and the comment
   becomes true.

Other observations (no findings):
- `HubClient`'s `baseURL` is always constructed as
  `"http://127.0.0.1:\(HubPaths.port)"` (HubClient.swift:87) - hardcoded
  loopback, matching the server-side "no way to reach a non-loopback hub"
  design (Area 1) on the client side too. Only the port is configurable
  (via `BEACON_PORT`, range-validated 1-65535, `HubPaths.swift:47-52`), never
  the host.
- `SnapshotCache` (SnapshotCache.swift) only ever reads/writes
  `snapshot.json` in the shared App Group container, and the token is never
  written there - matches the "only the snapshot is shared, never the hub
  token" comment (HubClient.swift:40-44) and is consistent with what
  `WidgetProvider.swift` actually calls.
- `ENABLE_HARDENED_RUNTIME: NO` is set project-wide (project.yml:16). This
  is expected and not flagged as a finding: `DEVELOPMENT_TEAM: ""` and
  `CODE_SIGN_IDENTITY: "-"` (project.yml:13-14) show this is an ad-hoc-signed
  local development build, not a notarized/Developer-ID or App Store
  distribution, and Hardened Runtime is a distribution-signing concern.
  Worth revisiting if Beacon is ever distributed outside the developer's own
  Mac.

---

## Area 7: dependency review and CI workflow permissions

Read: go.mod (26 lines, full), go.sum (present, 80 lines, integrity hashes
only - not read line by line), `.github/workflows/ci.yml` (96 lines, full),
`.github/dependabot.yml` (11 lines, full). Ran `go list -m all` (resolved
entirely from the local module cache, no network fetch occurred) to see the
full transitive graph.

**Dependencies.** Direct: `github.com/shirou/gopsutil/v4`,
`modernc.org/sqlite`. Transitive graph (via `go list -m all`) is the
expected set for those two: `modernc.org/{libc,cc,ccgo,mathutil,memory,...}`
(sqlite's pure-Go/no-cgo toolchain), `golang.org/x/{sys,sync}`, per-OS
gopsutil helpers (`go-ole`, `wmi`, `plan9stats`, `perfstat`), plus
`stretchr/testify` and `google/go-cmp` (test-only). No `replace`/`exclude`
directives in go.mod. `go.sum` is present, so module integrity is verified
against recorded hashes on every build. Nothing in the graph is an
unmaintained/typosquat-looking or unexpectedly-broad-permission package for
what this program does (SQLite driver + host-metrics library + their own
transitive deps). I do not have network access to a live vulnerability
database in this session, and several resolved versions in this tree
(`go 1.26.5` toolchain, `modernc.org/sqlite v1.57.0`, `gopsutil/v4 v4.26.7`,
`golang.org/x/sys v0.47.0`) are recent enough that I cannot responsibly
cross-reference them against specific CVEs from training data with
confidence - so I am not reporting a specific "known-vulnerable version"
finding here, per the instruction to prefer no finding over a speculative
one. What I'd need to check to go further: run `govulncheck ./...` with
network access to the Go vulnerability DB, or an OSV/Snyk-style scan against
this exact `go.sum`.
- This is already automated: `.github/workflows/ci.yml`'s `govulncheck` job
  (lines 48-57) installs and runs `govulncheck ./...` on every push and PR,
  which is precisely the right tool for this and removes the need for a
  manual point-in-time CVE cross-reference - I cannot see this session
  whether the most recent run passed (no network/GH Actions access), but the
  mechanism is present and correctly wired.
- `.github/dependabot.yml` watches both `gomod` and `github-actions`
  ecosystems weekly - dependency updates (including the Actions pinned in
  CI) are automated, not manual.

**CI workflow (ci.yml) - one real finding, otherwise clean.**

1. **[Low] No explicit `permissions:` block in the workflow.** Neither the
   workflow-level nor any job in `ci.yml` sets `permissions:`, so every
   job's `GITHUB_TOKEN` gets whatever this repository's default Actions
   token permission setting is (Settings > Actions > General > Workflow
   permissions), which GitHub still defaults to "read and write" for many
   repositories, especially older ones. None of the six jobs
   (gofmt/vet/test/govulncheck/resolver-parity/cross-compile-pi) write
   anything - no commit, tag, release, PR comment, or package publish - so
   the correct, least-privilege setting is `permissions: contents: read` at
   the workflow's top level, and none of them need more. I did not find a
   concrete script-injection or secrets-exfiltration path that this excess
   permission could currently be chained with (see below), so this is a
   hardening gap rather than a demonstrated exploit: the concrete risk it
   guards against is a future job or a compromised/added third-party Action
   being able to push, tag, or otherwise write to the repo with a token that
   has no reason to hold that power. Minimal fix: add
   `permissions: contents: read` at the top of ci.yml.
- Checked specifically for the two classic GitHub Actions supply-chain
  bugs and found neither: (a) **no `pull_request_target` trigger** - the
  workflow uses only `push` and `pull_request` (ci.yml:3-5), so a
  fork-submitted PR's workflow run gets a read-only, fork-scoped token and
  never sees repository secrets, which is the safe pattern; (b) **no
  untrusted `${{ github.event.* }}` interpolation in any `run:` shell
  block** - grepped every step; the only `${{ }}` expression anywhere is
  `${{ env.GO_VERSION }}`, and it's only ever used in a `with:` field
  (`go-version:`), never inside a `run:` string, so there is no
  attacker-controlled string (PR title, branch name, commit message) being
  substituted into a shell command that could inject arbitrary commands
  into the runner.
- No `secrets.*` reference anywhere in the workflow (grepped, zero hits) -
  there is nothing for excess permissions or a hypothetical injection to
  exfiltrate beyond the ambient `GITHUB_TOKEN` itself.
- Third-party actions used are only `actions/checkout@v4` and
  `actions/setup-go@v5`, both GitHub's own first-party actions pinned to
  major-version tags (not a floating branch, not a random marketplace
  action). Pinning to a full commit SHA is the stricter best practice, but
  given these are first-party GitHub actions the risk this closes is
  small; noted as a minor hardening opportunity, not ranked as a finding.
- `go install golang.org/x/vuln/cmd/govulncheck@latest` (ci.yml:56) installs
  an unpinned `@latest` tool from an official Go-team module before running
  it. Standard supply-chain hygiene would pin a version, but the package is
  maintained by the Go team itself and this is a very low realistic risk;
  noted, not ranked.
- `resolver-parity` job (ci.yml:59-81) runs the SSRF guard's test suite
  under both `CGO_ENABLED=1` (system/glibc resolver) and `CGO_ENABLED=0`
  with `GODEBUG=netdns=go` (pure-Go resolver). This is direct, running
  corroboration of the Area 3 finding that `ambiguousNumericHost` and the
  guard's resolver-parity reasoning are taken seriously - it is exactly the
  test the guard.go comments describe needing, actually wired into CI
  rather than only asserted in a comment.

No other findings from this area.

---

