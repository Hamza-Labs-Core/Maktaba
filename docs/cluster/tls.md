# The cluster link: encrypted, authenticated machine-to-home traffic

Everything a machine says to its home travels over **encrypted connections that prove which
machine is talking**: the machine authenticates first on one connection (its *control
channel*), and every stream (a TV channel the home reads from it, a release download, a
proxied socket) goes on a connection of its own, opened with a single-use ticket from the
control channel. Library media is still read straight from the NAS by each machine; it never
travels over the link (except the playback fallback when a machine cannot read a file
itself, which the Machines page shows).

Nothing has to be set up: the home makes its own small certificate authority (the **cluster
CA**) at first start, every machine gets a certificate from it, and the link comes up by
itself. Machines joined before this release move over on their next start, without
re-joining.

## How it works

- **The cluster CA** lives on the home: `cluster-ca.key` beside `secrets.key` (the server's
  keys folder), plus a sealed copy in the home's database, so restoring the home from a
  backup (the database and the secrets key) brings the same CA back and every machine's
  certificate stays valid. A switchover's or a standby's kit carries it sealed for the
  machine that may become the home, so the next home keeps the CA; a machine writes it only
  when it becomes the home, and a home that steps down wipes it. It is never written on the
  share or in a diagnostics bundle.
- **A machine's certificate** is made from a key the machine generates and never sends:
  at join, the request carries a certificate request and the approval returns the
  certificate (protected by the pairing code you typed: nothing is trusted on first use). A
  machine joined earlier asks for it on its existing authenticated channel. It lasts 30 days
  (`tls_cert_days`) and renews itself when a third of that is left. The files sit beside the
  machine's `node.json`: `cluster-node.key`, `cluster-node.crt`, `cluster-ca.crt`.
- **One port.** The link uses the home's existing API port (8080): the first byte of each
  connection tells TLS from plain HTTP, so browsers and apps keep working as before and no
  firewall rule changes.
- **Both sides check.** The home accepts only a certificate from its CA, unexpired, not
  revoked, naming an approved machine and being that machine's current certificate. The
  machine checks that the home's certificate comes from the CA it pinned and names the
  address it dialled.
- **Revoking a machine** closes its link and all its streams at once and refuses its
  certificate from then on (its database login is turned off as before).
- **A dropped link** (the home's API restarting) comes back by itself; open streams keep
  running for up to a minute (`link_stream_grace_seconds`) and carry on with the new
  connection, so TV does not stop.

## Modes

Settings → Rules → Machines → **Cluster link** (`link_mode`):

| Mode | What it means |
|---|---|
| `prefer` (default) | A machine with a certificate uses the link; the others use the older paths. |
| `require` | The older paths are refused: a machine's plain channel connection may only renew its certificate, signed HTTP requests from machines are refused, the home's TV front door reads machines only over the link, and a machine's streaming port refuses the network. The home's Postgres also asks each machine for its certificate. |
| `off` | The older paths only. |

The home switches from `prefer` to `require` **by itself** once every approved machine has
held its link for 24 hours without a break (`link_auto_require`); the Machines page shows how
many have, and sends a notification when it switches. To stop it, set the mode back to
`prefer` (or turn `link_auto_require` off). A machine that cannot hold the link (a network
device that strips WebSocket upgrades, say) simply keeps the switch from happening.

## The database

The home's own Postgres (the one Maktaba runs) verifies the machines' certificates from the
cluster CA, and once the link is required it asks each machine's database login for its
certificate as well: the certificate's name is the machine's database role, so a stolen
password alone is useless. Machines offer their certificate automatically.

If your home's database is a Postgres **you** run, run this on the home once its API has
started:

```bash
maktaba-server setup --pg-tls
```

It issues that server's certificate from the cluster CA and prints the `postgresql.conf` and
`pg_hba.conf` lines to apply there.

## HTTPS in the browser (optional)

Turn on `tls_web_https` (Settings → Rules → Machines → Cluster link): every machine then also
serves its pages on **port 8443** over HTTPS, with a certificate from the cluster CA. To open
them without a warning, install the CA on each device once. Download it from the Machines
page (or `http://<home>:8080/api/cluster/ca.crt`) and compare the fingerprint the Machines
page shows with the one your system displays before you trust it.

- **macOS:** open the file, add it to the *System* keychain in Keychain Access, open it there
  and set *When using this certificate* to **Always Trust**.
- **Windows:** open the file → *Install Certificate* → *Local Machine* → *Place all
  certificates in the following store* → **Trusted Root Certification Authorities**.
- **Linux:** copy it to `/usr/local/share/ca-certificates/maktaba-cluster-ca.crt` and run
  `sudo update-ca-certificates` (Firefox: Settings → Certificates → Import).
- **iOS:** open the file to install the profile, then Settings → General → About →
  Certificate Trust Settings → turn it on.
- **Android:** Settings → Security → Encryption & credentials → Install a certificate → CA
  certificate.

Browsers then accept every machine's HTTPS address, and sign-in cookies are marked secure.

## What to check

- **A machine stays on the older paths:** the Machines page lists its link and certificate.
  "no certificate yet" clears on its next connection to the home; a machine joined before the
  home had a cluster key must [re-join](troubleshooting.md#re-join-this-machine).
- **"certificate expired":** the machine was off for over a month; it renews by itself when
  it is back.
- **The home moved to another address:** its certificate is issued again for its addresses
  within five minutes; a machine that dials an address the certificate does not name refuses
  it and says so in its log.
- **A clock more than five minutes off** on a machine makes its new certificate "not yet
  valid": set the machine's clock (network time) and it connects.
