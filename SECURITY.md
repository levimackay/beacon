# Security policy

Beacon is designed for single-user, local or tailnet use. It is not hardened
for exposure to the public internet, and it should never be put behind a
port forward, a reverse proxy on `0.0.0.0`, or any other path that makes it
reachable from an untrusted network. If you find a way to make that
configuration necessary, that is itself a bug worth reporting.

## Reporting a vulnerability

Please report security issues privately through GitHub's security advisory
form for this repository (the "Security" tab, "Report a vulnerability"),
rather than opening a public issue. That gives us a private channel to work
out a fix before any details are public.

Include what you found, the code path or endpoint involved, and steps to
reproduce if you have them. There is no bug bounty; this is a personal
project.

## Scope

In scope:

- The `beaconhub` server: authentication, the HTTP API, the SSRF guard in
  `internal/collect`, the SQLite store, the audit log.
- The `beacon` CLI.
- Anything in this repository under `internal/` or `cmd/`.

Out of scope:

- The macOS app, widget and menu bar app, which do not exist yet (see
  README status).
- Vulnerabilities that require an attacker to already have your bearer
  token, root on your machine, or write access to your Tailscale network.
  Beacon's threat model assumes those are trusted.
- Denial of service against a hub that is only ever reachable on loopback
  or your own tailnet.

## Threat model, in plain terms

The hub binds `127.0.0.1` only; there is no code path that binds `0.0.0.0`
or a LAN address. Reaching the hub from another machine at all requires
`tailscale serve`, which puts Tailscale's own authentication and TLS in
front of it. Because the network already establishes who is talking to the
hub, Beacon does not implement its own login, session store, or account
system.

On top of that, every request still needs a bearer token, generated on
first run and stored in a file with mode `0600`. The token is compared with
a constant-time comparison, never logged, and redacted from any log line
whose key looks like a credential.

The other thing Beacon actively defends against is SSRF: a website target
is a URL the operator gives it, and without a guard that is a way to make
the hub fetch an arbitrary address, including the cloud metadata endpoint
or the hub's own loopback interface. The guard in `internal/collect/guard.go`
resolves the target's DNS, rejects loopback, private, carrier-grade NAT,
link-local, unique-local, unspecified, multicast and the cloud metadata
address, and repeats that check at the moment of the actual TCP dial (not
just once, ahead of time), which is what closes a DNS-rebinding attack
where the address changes between the check and the connection. A target
can opt in to reaching the operator's own private networks (`AllowPrivate`),
for monitoring something on your own LAN or tailnet, but that opt-in never
extends to the cloud metadata address.

Beacon executes no shell commands anywhere, and no endpoint accepts a
command, a path, or a filesystem location from a caller. Every mutating
request writes an audit row.

What is not yet defended against, because the corresponding feature does
not exist yet: multi-user access control (there is currently one caller,
authenticated by one token), and anything to do with the macOS app, widget,
or web dashboard.
