"""Storage servers registered from the home's mounts (story 31.3, automatic): recorded
``mount`` / ``/proc/mounts`` / ``net use`` outputs, the NetBIOS lookups' outputs."""

from __future__ import annotations

from maktaba_pipeline.sharepath.autoreg import (
    Lookups,
    parse_nbtstat,
    parse_nmblookup,
    parse_smbutil_status,
    plan_servers,
)
from maktaba_pipeline.sharepath.mounts import (
    parse_mac_mount,
    parse_net_use,
    parse_proc_mounts,
)
from maktaba_pipeline.sharepath.resolver import StorageServer

NAS_IP = "192.168.50.94"

MAC_MOUNT = """\
/dev/disk3s1s1 on / (apfs, sealed, local, read-only, journaled)
devfs on /dev (devfs, local, nobrowse)
//admin@192.168.50.94/Originals on /Volumes/Originals (smbfs, nodev, nosuid, mounted by me)
//admin@Home-NAS._smb._tcp.local/Media on /Volumes/Media (smbfs, nodev, nosuid, mounted by me)
//guest@10.0.0.7/Backups on /Volumes/Backups (smbfs, nodev, nosuid, mounted by me)
"""

LINUX_PROC = """\
/dev/sda1 / ext4 rw,relatime 0 0
//192.168.50.94/Originals /mnt/originals cifs rw,vers=3.1.1 0 0
192.168.50.94:/volume1/Media /srv/media nfs4 rw,relatime 0 0
"""

WINDOWS_NET_USE = """\
New connections will be remembered.


Status       Local     Remote                    Network

-------------------------------------------------------------------------------
OK           O:        \\\\Home-NAS\\Originals     Microsoft Windows Network
OK           Y:        \\\\Home-NAS\\Originals\\Backup\\Youtube
                                                Microsoft Windows Network
OK                     \\\\192.168.50.94\\IPC$     Microsoft Windows Network
The command completed successfully.
"""

SMBUTIL = """\
Workgroup: WORKGROUP
Server: HOME-NAS
"""

NMBLOOKUP = """\
Looking up status of 192.168.50.94
\tHOME-NAS        <00> -         B <ACTIVE>
\tHOME-NAS        <03> -         B <ACTIVE>
\tWORKGROUP       <00> - <GROUP> B <ACTIVE>

\tMAC Address = 00-11-32-00-00-00
"""

NBTSTAT = """\

Ethernet:
Node IpAddress: [192.168.50.10] Scope Id: []

           NetBIOS Remote Machine Name Table

       Name               Type         Status
    ---------------------------------------------
    HOME-NAS       <00>  UNIQUE      Registered
    WORKGROUP      <00>  GROUP       Registered
    HOME-NAS       <20>  UNIQUE      Registered
"""


def _lookups(
    *,
    reverse: dict[str, list[str]] | None = None,
    netbios: str | None = None,
    names: tuple[str, ...] = ("home-nas", "home-nas.local", "home-nas.lan"),
) -> Lookups:
    nas_names = {NAS_IP, *names}

    def resolve(host: str) -> frozenset[str]:
        h = host.lower()
        if h in nas_names:
            return frozenset({NAS_IP})
        if h == "10.0.0.7":
            return frozenset({h})
        return frozenset()

    return Lookups(
        resolve=resolve,
        reverse=lambda ip: list((reverse or {}).get(ip, [])),
        netbios=lambda ip: netbios if ip == NAS_IP else None,
    )


def test_netbios_outputs() -> None:
    assert parse_smbutil_status(SMBUTIL) == "HOME-NAS"
    assert parse_nmblookup(NMBLOOKUP) == "HOME-NAS"
    assert parse_nbtstat(NBTSTAT) == "HOME-NAS"
    assert parse_smbutil_status("") is None
    assert parse_nmblookup("name_query failed to find name 10.0.0.9") is None
    assert parse_nbtstat("Host not found.") is None


def test_mac_mounts_by_ip_and_bonjour_are_one_server() -> None:
    mounts = parse_mac_mount(MAC_MOUNT)
    planned, skipped = plan_servers(mounts, [], _lookups(reverse={NAS_IP: ["Home-NAS.lan"]}))
    assert skipped == []
    by = {s.name: s for s in planned}
    assert set(by) == {"home-nas", "nas"}
    nas = by["home-nas"]
    # every host the mounts use, the names found and the IP
    # (the short name is the record's name: it matches without being an address)
    assert nas.addresses == (NAS_IP, "home-nas.local", "Home-NAS.lan")
    # the other server answers no name: "nas"
    assert by["nas"].addresses == ("10.0.0.7",)


def test_a_mount_by_ip_without_dns_is_named_by_netbios() -> None:
    mounts = parse_mac_mount(MAC_MOUNT)[:1]
    [s], _ = plan_servers(mounts, [], _lookups(netbios="HOME-NAS"))
    assert s.name == "home-nas"
    assert s.addresses == (NAS_IP, "HOME-NAS")


def test_linux_smb_and_nfs_mounts_of_one_nas() -> None:
    mounts = parse_proc_mounts(LINUX_PROC)
    [s], _ = plan_servers(mounts, [], _lookups(reverse={NAS_IP: ["home-nas.lan"]}))
    assert s.name == "home-nas"
    assert s.addresses == (NAS_IP, "home-nas.lan")


def test_windows_drives_by_netbios_name() -> None:
    mounts = parse_net_use(WINDOWS_NET_USE)
    [s], _ = plan_servers(mounts, [], _lookups())
    assert s.name == "home-nas"
    assert s.addresses == ("home-nas", NAS_IP)


def test_a_known_server_is_never_changed_and_a_removed_one_not_added_again() -> None:
    mounts = parse_mac_mount(MAC_MOUNT)
    known = StorageServer(name="synology", addresses=("home-nas.lan",))
    planned, _ = plan_servers(mounts, [known], _lookups())
    # 192.168.50.94 and the Bonjour name resolve to an address the record has: matched
    assert [s.name for s in planned] == ["nas"]
    # the admin removed what the home registered for 10.0.0.7: not registered again
    planned, skipped = plan_servers(mounts, [known], _lookups(), registered={"10.0.0.7": "nas"})
    assert planned == [] and skipped == ["10.0.0.7"]


def test_names_are_made_unique() -> None:
    mounts = parse_mac_mount(MAC_MOUNT)[:1]
    # another machine already called "home-nas" (the name does not resolve here)
    taken = StorageServer(name="home-nas", addresses=("10.9.9.9",))
    lookups = _lookups(reverse={NAS_IP: ["home-nas.lan"]}, names=("home-nas.lan",))
    [s], _ = plan_servers(mounts, [taken], lookups)
    assert s.name == "home-nas-2"
