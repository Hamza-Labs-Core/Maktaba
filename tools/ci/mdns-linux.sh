#!/usr/bin/env bash
# mdns-linux.sh: the mDNS announcement through systemd-resolved, for real, on a CI runner
# (the mdns-linux job, _unit.yml). Never on a developer's machine: it reconfigures resolved
# and masks Avahi.
#
#   mdns-linux.sh setup        resolved answers mDNS (MulticastDNS=yes, globally and on a
#                              veth to a peer namespace, where the test's client asks),
#                              Avahi stopped and masked (Avahi would win), polkit running;
#                              builds both copies' test binaries
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

# the peer's namespace and the veth to it (this end, its end)
peer_ns=mdnspeer
host_if=mkhost0
peer_if=mkpeer0

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
  # debug logging that outlives a restart (resolvectl log-level does not)
  sudo mkdir -p /run/systemd/system/systemd-resolved.service.d
  printf '[Service]\nEnvironment=SYSTEMD_LOG_LEVEL=debug\n' | sudo tee /run/systemd/system/systemd-resolved.service.d/50-maktaba-ci-debug.conf >/dev/null
  sudo systemctl daemon-reload
  sudo systemctl restart systemd-resolved
  # the peer: what resolved takes for another machine, since it ignores every mDNS packet
  # from an address of its own (resolved-mdns.c, on_mdns_packet): a namespace at the other
  # end of a veth. Not named ve-*: networkd would manage it (80-container-ve.network).
  sudo ip netns add "$peer_ns"
  sudo ip link add "$host_if" type veth peer name "$peer_if"
  sudo ip link set "$peer_if" netns "$peer_ns"
  sudo ip addr add 192.168.77.1/24 dev "$host_if"
  sudo ip link set "$host_if" multicast on up
  sudo ip netns exec "$peer_ns" ip link set lo up
  sudo ip netns exec "$peer_ns" ip addr add 192.168.77.2/24 dev "$peer_if"
  sudo ip netns exec "$peer_ns" ip link set "$peer_if" multicast on up
  sudo ip netns exec "$peer_ns" ip route add 224.0.0.0/4 dev "$peer_if"
  sudo resolvectl mdns "$host_if" yes
  local n
  for n in $(seq 1 20); do
    resolvectl status "$host_if" | grep -q 'mDNS/IPv4' && break
    sleep 0.5
  done
  echo "the peer's link: $host_if ($(resolvectl mdns "$host_if"), after $n tries)"
  resolvectl status "$host_if" | grep -q 'mDNS/IPv4' || { echo "::error::resolved does not answer mDNS on $host_if"; exit 1; }
  systemctl --version | sed -n 1p
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
  local status=0 t who pid
  who=$(id -un)
  [ ${#as[@]} -eq 0 ] || who=root
  pid=$(systemctl show -p MainPID --value systemd-resolved)
  for t in discovery mdns; do
    echo "== $t.test, $case (as $who)"
    "${as[@]}" env MAKTABA_TEST_LIVE_MDNS="$mode" MAKTABA_TEST_MDNS_IFACE="$host_if" \
      MAKTABA_TEST_MDNS_PEER_NETNS="$peer_ns" MAKTABA_TEST_MDNS_PEER_IFACE="$peer_if" \
      "$bin/$t.test" -test.run '^TestAnnounceLive$' -test.v -test.count=1 -test.timeout=5m \
      2>&1 | tee "$logs/$t-$case.log" || status=1
  done
  sudo rm -f "$rule"
  # an announcement must never bring resolved down (a name it could not read aborted it)
  if [ "$(systemctl show -p MainPID --value systemd-resolved)" != "$pid" ]; then
    echo "::error::systemd-resolved restarted during the $case run (it crashed: see its journal)"
    status=1
  fi
  return $status
}

logs() {
  {
    echo "== resolvectl status"; resolvectl status || true
    echo "== resolved's journal"; sudo journalctl -u systemd-resolved --no-pager -b -n 3000 || true
    echo "== polkit's journal"; sudo journalctl -u polkit --no-pager -b -n 300 || true
    echo "== who holds 5353"; sudo ss -ulpn 'sport = :5353' || true
    echo "== the peer"; ip addr show "$host_if" || true; sudo ip netns exec "$peer_ns" ip addr || true
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
