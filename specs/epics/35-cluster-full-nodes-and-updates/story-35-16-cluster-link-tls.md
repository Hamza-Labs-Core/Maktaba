# Story 35.16 — The cluster link: machine-to-machine TLS

## Description

*As the owner, I want everything my machines say to the home to travel over encrypted sockets that
prove which machine is talking — the machine authenticates first on one channel, and every stream
then goes on its own connection — while my library media keeps being read straight from the NAS.*

The home runs a small internal CA and gives every machine a certificate at join. Each machine keeps
**one control channel** to the home — a WebSocket over mutual TLS on the home's existing API port —
which authenticates first and carries control, commands, session checks, proxied `home` routes, the
service door and small requests. Every **data stream** (TV between the front door and a streaming
node, push-relay pulls, release and wheelhouse downloads, backup transfers, the VOD fallback) opens
its own `wss://` connection with a single-use ticket issued on the control channel. Postgres keeps
its own connection, now `verify-full` with a client certificate. Browsers are unchanged (optional
HTTPS). Machines joined today move over without re-joining.

Design: [design.md](design.md) §18 (and §11.5 for media); README R12, R13, decision "Machine ↔ home
transport".

## Acceptance criteria

- **AC1** CA: created on the home at first start (ECDSA P-256, 10 years), key `cluster-ca.key` 0600
  beside `secrets.key` plus a sealed copy in `home_secrets`; never written on a node, the share or a
  diagnostics bundle. `cluster_certs` records every certificate.
- **AC2** Join: the join request carries a CSR (key generated on the machine); the sealed approval
  bundle carries `tls {ca_cert, ca_sha256, cert}`; the certificate has SAN `maktaba://node/<id>`, CN
  = the node's database role, 30 days, `not_before` − 5 min.
- **AC3** Existing machines: after updating, a machine with a v2 agent session sends `cert.csr` and
  receives its certificate and the CA on that authenticated session — no re-join. A machine without
  `home.cluster_pub` is told to re-join.
- **AC4** Control channel: TLS 1.3 to the API port (TLS sniffed on the first byte; plain HTTP still
  served to browsers and apps on the same port), client certificate required for
  `/api/cluster/link`; the home checks chain, expiry, revocation and that the SAN node id is approved
  with that `cert_serial`; the node checks the server certificate against the pinned CA and the
  address it dialled. Nothing else is accepted before authentication.
- **AC5** Control framing: binary messages `{id, kind, class, len}` with payload ≤ 16 KiB; class
  `control` is always sent before class `rpc`; answers over 1 MiB move to a data stream. The home
  proxy's `RoundTripper` carries `home` routes over it unchanged in behaviour (status, headers,
  cookies without `Domain`, streaming bodies, cancel).
- **AC6** Tickets: `stream.open` / `stream.offer` → `stream.ticket` (32 random bytes, 30 s, single
  use, bound to node id, certificate serial, control session, purpose and resource). A stream
  connection presents mTLS + `Authorization: Maktaba-Ticket …`; the home refuses an invalid, expired,
  reused or foreign ticket, or any stream while the machine has no live control session; reuse is
  logged and alerted (3 in 10 min).
- **AC7** Streams: purposes `tv_pull`, `relay_pull`, `artifact`, `wheelhouse`, `backup`, `vod`,
  `svc_body`, `ws_proxy`; nodes always dial (the home never connects to a node); chunks ≤ 64 KiB;
  `cluster.link.max_streams` (32) per machine, refused with `limit`.
- **AC8** Front door ↔ streaming node: the front door's upstream for a node-encoded channel (and the
  push relay's source pull) is a `tv_pull` / `relay_pull` stream the node opens on `stream.offer`;
  in `require` the node's streaming port binds loopback only.
- **AC9** Reconnect: control channel backoff 1 s → 30 s with jitter; in-flight requests fail with
  `link_lost`, idempotent ones retry once; open streams survive up to `stream_grace_seconds` (60) and
  re-attach to the machine's new control session, else close (TV fails over through Epic 32/33).
- **AC10** Revocation: revoking a machine closes its control channel (`4010`) and all its streams at
  once, refuses its certificate serial, and its database role is `NOLOGIN` (Epic 31).
- **AC11** Renewal at a third of the lifetime over `cert.renew`; an expired certificate renews over
  the v2 session (only `cert.*` is allowed there in `require`).
- **AC12** Postgres: `maktaba-server setup --pg-tls` configures the home's server TLS (cluster CA,
  `pg_server` certificate); nodes connect `sslmode=verify-full` with their certificate; in `require`
  `pg_hba` uses `hostssl … clientcert=verify-full` for `maktaba_node_*`.
- **AC13** Mode: `cluster.link.mode` `prefer` by default, switched to `require` automatically after
  every approved machine held a control session for 24 h (shown on the Machines page; admin
  override); in `require` the legacy plain agent WebSocket from nodes, the delegation HMAC over HTTP
  and front-door HTTP to node streaming ports are refused.
- **AC14** Media: no library media is read through the link except the VOD fallback (design §11.5),
  which the Machines page reports.
- **AC15** Optional browser HTTPS: `cluster.tls.web_https` serves each machine's web UI on 8443 with a
  cluster-CA certificate; `GET /api/cluster/ca.crt` and the fingerprint on the Machines page; docs per
  OS for installing the CA.

## Test cases

- **TC1** `TestCAIssueRenewRevoke`, `TestLeafSANAndCN`, `TestCAKeyNeverServed`.
- **TC2** `TestTLSSnifferServesBothOnOnePort`, `TestLinkRefusesNoCert`, `TestLinkRefusesWrongCA`,
  `TestLinkRefusesRevokedSerial`, `TestNodeRefusesHomeCertWrongSAN`.
- **TC3** `TestCertCSROverV2SessionNoRejoin` (a node joined before this story).
- **TC4** `TestControlFramingClassOrder` (an `rpc` flood never delays a `control` frame by more than one
  message), `TestControlPayloadCap`, `TestLargeAnswerMovesToStream`.
- **TC5** `TestRoundTripperOverControl` (headers, cookies, cancel, streaming body).
- **TC6** `TestTicketSingleUse`, `TestTicketExpiry`, `TestTicketWrongNode`, `TestTicketWrongPurpose`,
  `TestTicketNoControlSession`, `TestTicketReuseAlert`, `TestMaxStreams`.
- **TC7** `TestStreamSurvivesControlReconnect` (re-attached within 60 s), `TestStreamClosedAfterGrace`,
  `TestRevokeClosesAllStreamsAtOnce`.
- **TC8** `TestFrontDoorUpstreamOverTicketedStream` (TS splice/HLS unchanged), `TestNodeStreamingPortLoopbackInRequire`.
- **TC9** Integration: Postgres with `verify-full` and client certificates; a stolen password without
  the certificate is refused.
- **TC10** `TestLinkModeAutoRequire`, `TestLegacyPathsRefusedInRequire`.
- **TC11** Load: 20 TV pulls + a release download on one machine; control-channel p99 < 50 ms.
- **TC12** Python: the pipeline agent connects through the supervisor's loopback endpoint with its v2
  sealing unchanged (`test_agent_via_local_link`).

## Edge cases

- **EC1** The home's address changes (DHCP): its server certificate is re-issued with the new SANs;
  nodes reconnect; a node that dials an address not in the SANs refuses and reports it.
- **EC2** Clock skew > 5 min on a node: certificate not yet valid; reported as `clock_skew` on the
  Machines page; renewal waits.
- **EC3** A middlebox strips WebSocket upgrades on the LAN: the machine stays on the legacy paths in
  `prefer`, and the Machines page blocks the switch to `require` with the reason.
- **EC4** A node offline for over 30 days: its certificate expired; it renews over the v2 session on
  return.
- **EC5** The home's Postgres is not managed by Maktaba (another admin): `setup --pg-tls` prints the
  configuration instead of applying it; `require` waits until a node's `verify-full` connection works.
- **EC6** Restore of the home from backup: the CA key comes back from the sealed copy, so existing
  machine certificates stay valid.

## Dependencies

Epic 31 phase 2 (v2 agent session, cluster key), 31.3 (share-path resolver); Epic 32 (front door,
streaming nodes); 35.8 (machine agent), 35.9 and 35.10 (their requests move onto the control channel),
35.12 (artifact streams).
