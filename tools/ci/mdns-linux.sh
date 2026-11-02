#!/usr/bin/env bash
# mdns-linux.sh: the mDNS announcement through systemd-resolved, for real, on a CI runner
# (the mdns-linux job, _unit.yml). Never on a developer's machine: it reconfigures resolved
# and masks Avahi.
#
#   mdns-linux.sh setup        resolved answers mDNS (MulticastDNS=yes, globally and on the
#                              default route's link), Avahi stopped and masked (Avahi would
#                              win), polkit running; builds both copies' test binaries
#   mdns-linux.sh run <case>   runs TestAnnounceLive of both copies:
#                                denied   as this user, no polkit rule: zeroconf under
#                                         "<host>-maktaba"
#                                allowed  as this user, a polkit rule lets it register:
#                                         resolved's RegisterService
#                                root     as root: RegisterService
#   mdns-linux.sh logs         resolvectl status, resolved's and polkit's journals, into
#                              $LOGS (default $RUNNER_TEMP/mdns-linux-logs) and stdout
set -euo pipefail

root=$(cd "$(dirname "$0")/../.." && pwd)
tmp=${RUNNER_TEMP:-/tmp}
bin="$tmp/mdns-linux-bin"
logs=${LOGS:-$tmp/mdns-linux-logs}
rule=/etc/polkit-1/rules.d/50-maktaba-ci-register-service.rules
mkdir -p "$logs"

iface() { ip -o route show default | awk '{print $5; exit}'; }

setup() {
  [ "${CI:-}" = "true" ] || { echo "mdns-linux.sh reconfigures resolved: CI only" >&2; exit 2; }
  for u in avahi-daemon.socket avahi-daemon.service; do
    sudo systemctl stop "$u" 2>/dev/null || true
    sudo systemctl mask "$u" 2>/dev/null || true
  done
  for s in /run/avahi-daemon/socket /var/run/avahi-daemon/socket; do
    if [ -e "$s" ]; then echo "::error::Avahi still listens at $s"; exit 1; fi
  done
  if ! command -v pkcheck >/dev/null || ! systemctl cat polkit.service >/dev/null 2>&1; then
    "$root/tools/ci/apt-install.sh" polkitd
  fi
  sudo systemctl start polkit.service
  sudo rm -f "$rule"

  sudo mkdir -p /etc/systemd/resolved.conf.d
  printf '[Resolve]\nMulticastDNS=yes\n' | sudo tee /etc/systemd/resolved.conf.d/50-maktaba-ci-mdns.conf >/dev/null
  sudo systemctl restart systemd-resolved
  sudo resolvectl log-level debug
  local i
  i=$(iface)
  sudo resolvectl mdns "$i" yes
  echo "the default route's link: $i"
  [ -n "${GITHUB_ENV:-}" ] && echo "MAKTABA_TEST_MDNS_IFACE=$i" >>"$GITHUB_ENV"
  systemctl --version | head -1
  resolvectl status

  mkdir -p "$bin"
  (cd "$root/api" && go test -c -o "$bin/discovery.test" ./internal/discovery/)
  (cd "$root/cmd/maktaba-server" && go test -c -o "$bin/mdns.test" ./internal/mdns/)
}

run() {
  local case=$1 mode as=()
  case "$case" in
    denied) mode=own-host; sudo rm -f "$rule" ;;
    allowed)
      mode=resolved
      sudo tee "$rule" >/dev/null <<EOF
polkit.addRule(function(action, subject) {
  if (action.id == "org.freedesktop.resolve1.register-service" && subject.user == "$(id -un)") {
    return polkit.Result.YES;
  }
});
EOF
      sleep 2 # polkit reads a new rule within a moment
      ;;
    root) mode=resolved; sudo rm -f "$rule"; as=(sudo) ;;
    *) echo "usage: mdns-linux.sh run denied|allowed|root" >&2; exit 2 ;;
  esac
  local i=${MAKTABA_TEST_MDNS_IFACE:-$(iface)} status=0 t who
  who=$(id -un)
  [ ${#as[@]} -eq 0 ] || who=root
  for t in discovery mdns; do
    echo "== $t.test, $case (as $who)"
    "${as[@]}" env MAKTABA_TEST_LIVE_MDNS="$mode" MAKTABA_TEST_MDNS_IFACE="$i" \
      "$bin/$t.test" -test.run '^TestAnnounceLive$' -test.v -test.count=1 -test.timeout=5m \
      2>&1 | tee "$logs/$t-$case.log" || status=1
  done
  sudo rm -f "$rule"
  return $status
}

logs() {
  {
    echo "== resolvectl status"; resolvectl status || true
    echo "== resolved's journal"; sudo journalctl -u systemd-resolved --no-pager -b -n 3000 || true
    echo "== polkit's journal"; sudo journalctl -u polkit --no-pager -b -n 300 || true
    echo "== who holds 5353"; sudo ss -ulpn 'sport = :5353' || true
  } 2>&1 | tee "$logs/system.log"
}

cmd=${1:-}
shift || true
case "$cmd" in
  setup) setup ;;
  run) run "$@" ;;
  logs) logs ;;
  *) echo "usage: mdns-linux.sh setup | run denied|allowed|root | logs" >&2; exit 2 ;;
esac
