#!/bin/sh
# Post-install hook: create the `maktaba` user, lock down the config
# file, and offer the first-run wizard if no claim secret is present.
set -e
if ! id maktaba >/dev/null 2>&1; then
    useradd --system --no-create-home --shell /usr/sbin/nologin maktaba
fi
mkdir -p /var/lib/maktaba /var/log/maktaba
chown -R maktaba:maktaba /var/lib/maktaba /var/log/maktaba
chmod 0640 /etc/maktaba/server.toml || true
chown root:maktaba /etc/maktaba/server.toml || true

systemctl daemon-reload || true
systemctl enable maktaba-server || true

if [ ! -s /var/lib/maktaba/secret ]; then
    cat <<MSG

  Maktaba server installed but not linked to a cloud account yet.
  Run the first-run wizard to claim it:

      sudo maktaba-server setup

  Or get a claim code from https://app.maktaba.app -> "Add server".
MSG
fi
