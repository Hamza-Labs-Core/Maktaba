//! Linux: in-process, no setuid helper (story 54.03 §3).
//!
//!  1. `PR_SET_NO_NEW_PRIVS`.
//!  2. When unprivileged user namespaces work: new user, network and IPC namespaces, the
//!     user mapped to itself, the new network namespace left with no interface up. Tried
//!     first in a forked child (the process is still single-threaded), so a kernel or
//!     AppArmor that refuses half-way never leaves the host unmapped. Refused → recorded
//!     as missing `namespaces`. (Mount isolation is Landlock's job below; no pivot_root.)
//!  3. Landlock (ABI ≥ 1): read the plugins folder, read and write the cache and run
//!     folders, nothing else; with ABI ≥ 4 also no TCP bind or connect. Not in force →
//!     missing `landlock`.
//!  4. Always seccomp-bpf: `socket` and `socketpair` only for AF_UNIX (EPERM for every
//!     other family), and EPERM for ptrace, process_vm_*, bpf, perf_event_open, mount,
//!     keyctl, kexec, modules, userfaultfd, io_uring, namespaces, exec and fork (a clone
//!     without CLONE_THREAD); clone3 answers ENOSYS so threads use clone. A filter that
//!     cannot be installed ends the host (exit 78).
//!  5. Resource limits: RLIMIT_NOFILE 256, RLIMIT_CORE 0, RLIMIT_AS sized to the pool.
//!
//! The filter denies by name rather than allowing by name: an allow-list breaks with each
//! libc that starts using a new syscall, and the network refusal (the point of this layer)
//! is the socket-family rule, which is exact.

use std::collections::BTreeMap;

use landlock::{
    path_beneath_rules, Access, AccessFs, AccessNet, Ruleset, RulesetAttr, RulesetCreatedAttr, RulesetStatus, ABI,
};
use seccompiler::{
    BpfProgram, SeccompAction, SeccompCmpArgLen, SeccompCmpOp, SeccompCondition, SeccompFilter, SeccompRule, TargetArch,
};

use super::{injected, unavailable, Applied, Paths, Unavailable};

#[cfg(target_arch = "x86_64")]
const ARCH: TargetArch = TargetArch::x86_64;
#[cfg(target_arch = "aarch64")]
const ARCH: TargetArch = TargetArch::aarch64;

pub fn apply(p: &Paths) -> Result<Applied, Unavailable> {
    let mut missing = Vec::new();
    injected("no_new_privs")?;
    // SAFETY: plain prctl with constant arguments
    if unsafe { libc::prctl(libc::PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) } != 0 {
        return Err(unavailable("no_new_privs", std::io::Error::last_os_error()));
    }
    if injected("namespaces").is_err() || !namespaces() {
        missing.push("namespaces".to_string());
    }
    match landlock(p) {
        Ok(true) => {}
        Ok(false) => missing.push("landlock".to_string()),
        Err(e) => return Err(e),
    }
    limits(p)?;
    seccomp()?;
    Ok(Applied { level: if missing.is_empty() { "full".into() } else { "partial".into() }, missing })
}

fn write_file(path: &str, text: &str) -> bool {
    std::fs::write(path, text).is_ok()
}

fn enter_namespaces(uid: u32, gid: u32) -> bool {
    // SAFETY: unshare with constant flags; the process is single-threaded here
    if unsafe { libc::unshare(libc::CLONE_NEWUSER | libc::CLONE_NEWNET | libc::CLONE_NEWIPC) } != 0 {
        return false;
    }
    write_file("/proc/self/setgroups", "deny")
        && write_file("/proc/self/uid_map", &format!("{uid} {uid} 1"))
        && write_file("/proc/self/gid_map", &format!("{gid} {gid} 1"))
}

/// Enters the namespaces only if a forked child could: a refusal after unshare would
/// otherwise leave this process unmapped (its own files owned by "nobody").
fn namespaces() -> bool {
    // SAFETY: getuid/getgid cannot fail
    let (uid, gid) = unsafe { (libc::getuid(), libc::getgid()) };
    // SAFETY: single-threaded; the child only calls async-signal-safe-enough code and exits
    let pid = unsafe { libc::fork() };
    if pid < 0 {
        return false;
    }
    if pid == 0 {
        let ok = enter_namespaces(uid, gid);
        // SAFETY: leave the child at once, without running the parent's destructors
        unsafe { libc::_exit(if ok { 0 } else { 1 }) };
    }
    let mut status = 0;
    // SAFETY: waits for the child just forked
    if unsafe { libc::waitpid(pid, &mut status, 0) } != pid
        || !libc::WIFEXITED(status)
        || libc::WEXITSTATUS(status) != 0
    {
        return false;
    }
    enter_namespaces(uid, gid)
}

/// Answers whether Landlock is in force (fully or in part).
fn landlock(p: &Paths) -> Result<bool, Unavailable> {
    if injected("landlock").is_err() {
        return Ok(false);
    }
    let abi = ABI::V4;
    let err = |e: landlock::RulesetError| unavailable("landlock", e);
    let status = Ruleset::default()
        .handle_access(AccessFs::from_all(abi))
        .map_err(err)?
        .handle_access(AccessNet::BindTcp | AccessNet::ConnectTcp)
        .map_err(err)?
        .create()
        .map_err(err)?
        .add_rules(path_beneath_rules([&p.plugins_dir], AccessFs::from_read(abi)))
        .map_err(err)?
        .add_rules(path_beneath_rules([&p.cache_dir, &p.run_dir], AccessFs::from_all(abi)))
        .map_err(err)?
        .restrict_self()
        .map_err(err)?;
    Ok(status.ruleset != RulesetStatus::NotEnforced)
}

fn setrlimit(res: libc::__rlimit_resource_t, n: u64) -> Result<(), Unavailable> {
    let lim = libc::rlimit { rlim_cur: n, rlim_max: n };
    // SAFETY: a plain setrlimit with a valid struct
    if unsafe { libc::setrlimit(res, &lim) } != 0 {
        return Err(unavailable("rlimit", std::io::Error::last_os_error()));
    }
    Ok(())
}

fn limits(p: &Paths) -> Result<(), Unavailable> {
    injected("rlimit")?;
    setrlimit(libc::RLIMIT_NOFILE, 256)?;
    setrlimit(libc::RLIMIT_CORE, 0)?;
    // Wasmtime reserves address space per linear memory (4 GiB plus guards); a job may
    // hold up to 4 memories. Native code beyond that is capped at 2 GiB.
    let per_job: u64 = 4 * (4u64 << 30) + (256 << 20);
    setrlimit(libc::RLIMIT_AS, (2u64 << 30) + per_job * (p.pool_size.max(1) as u64 + 1))
}

/// The rules (syscall → conditions; an empty list matches always) of the EPERM filter.
pub fn deny_rules() -> BTreeMap<i64, Vec<SeccompRule>> {
    let mut rules: BTreeMap<i64, Vec<SeccompRule>> = BTreeMap::new();
    let not_unix = || {
        vec![SeccompRule::new(vec![SeccompCondition::new(
            0,
            SeccompCmpArgLen::Dword,
            SeccompCmpOp::Ne,
            libc::AF_UNIX as u64,
        )
        .unwrap()])
        .unwrap()]
    };
    rules.insert(libc::SYS_socket, not_unix());
    rules.insert(libc::SYS_socketpair, not_unix());
    // a clone that is not a thread is a new process
    rules.insert(
        libc::SYS_clone,
        vec![SeccompRule::new(vec![SeccompCondition::new(
            0,
            SeccompCmpArgLen::Qword,
            SeccompCmpOp::MaskedEq(libc::CLONE_THREAD as u64),
            0,
        )
        .unwrap()])
        .unwrap()],
    );
    let always: &[i64] = &[
        libc::SYS_ptrace,
        libc::SYS_process_vm_readv,
        libc::SYS_process_vm_writev,
        libc::SYS_bpf,
        libc::SYS_perf_event_open,
        libc::SYS_mount,
        libc::SYS_umount2,
        libc::SYS_pivot_root,
        libc::SYS_chroot,
        libc::SYS_keyctl,
        libc::SYS_add_key,
        libc::SYS_request_key,
        libc::SYS_kexec_load,
        libc::SYS_kexec_file_load,
        libc::SYS_init_module,
        libc::SYS_finit_module,
        libc::SYS_delete_module,
        libc::SYS_userfaultfd,
        libc::SYS_io_uring_setup,
        libc::SYS_io_uring_enter,
        libc::SYS_io_uring_register,
        libc::SYS_execve,
        libc::SYS_execveat,
        libc::SYS_unshare,
        libc::SYS_setns,
        #[cfg(target_arch = "x86_64")]
        libc::SYS_fork,
        #[cfg(target_arch = "x86_64")]
        libc::SYS_vfork,
    ];
    for s in always {
        rules.insert(*s, vec![]);
    }
    rules
}

pub fn programs() -> Result<(BpfProgram, BpfProgram), String> {
    let deny = SeccompFilter::new(deny_rules(), SeccompAction::Allow, SeccompAction::Errno(libc::EPERM as u32), ARCH)
        .map_err(|e| e.to_string())?;
    let mut clone3 = BTreeMap::new();
    clone3.insert(libc::SYS_clone3, vec![]);
    let clone3 = SeccompFilter::new(clone3, SeccompAction::Allow, SeccompAction::Errno(libc::ENOSYS as u32), ARCH)
        .map_err(|e| e.to_string())?;
    Ok((
        BpfProgram::try_from(deny).map_err(|e| e.to_string())?,
        BpfProgram::try_from(clone3).map_err(|e| e.to_string())?,
    ))
}

fn seccomp() -> Result<(), Unavailable> {
    injected("seccomp")?;
    let (deny, clone3) = programs().map_err(|e| unavailable("seccomp", e))?;
    seccompiler::apply_filter(&clone3).map_err(|e| unavailable("seccomp", e))?;
    seccompiler::apply_filter(&deny).map_err(|e| unavailable("seccomp", e))?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    /// T04: the filter refuses every socket family but AF_UNIX, and compiles to BPF.
    #[test]
    fn socket_rule_is_unix_only() {
        let rules = deny_rules();
        let socket = &rules[&libc::SYS_socket];
        assert_eq!(socket.len(), 1);
        let dbg = format!("{socket:?}");
        assert!(dbg.contains("Ne") && dbg.contains(&format!("{}", libc::AF_UNIX)), "{dbg}");
        for s in [libc::SYS_execve, libc::SYS_ptrace, libc::SYS_bpf, libc::SYS_mount, libc::SYS_unshare] {
            assert!(rules[&s].is_empty(), "{s} is denied always");
        }
        let (deny, clone3) = programs().unwrap();
        assert!(deny.len() > 20 && !clone3.is_empty());
    }

    /// The filter in a forked child: AF_INET, AF_INET6, AF_NETLINK and AF_PACKET answer
    /// EPERM; AF_UNIX still works.
    #[test]
    fn filter_refuses_network_families() {
        // built before the fork: the child of a threaded test process must not allocate
        let (deny, clone3) = programs().unwrap();
        // SAFETY: the child installs the filter on itself and exits
        let pid = unsafe { libc::fork() };
        assert!(pid >= 0);
        if pid == 0 {
            let code = (|| {
                seccompiler::apply_filter(&clone3).ok()?;
                seccompiler::apply_filter(&deny).ok()?;
                for fam in [libc::AF_INET, libc::AF_INET6, libc::AF_NETLINK, libc::AF_PACKET] {
                    // SAFETY: socket() with constants
                    let fd = unsafe { libc::socket(fam, libc::SOCK_DGRAM, 0) };
                    if fd >= 0 || std::io::Error::last_os_error().raw_os_error() != Some(libc::EPERM) {
                        return Some(2);
                    }
                }
                // SAFETY: as above
                let fd = unsafe { libc::socket(libc::AF_UNIX, libc::SOCK_STREAM, 0) };
                Some(if fd >= 0 { 0 } else { 3 })
            })()
            .unwrap_or(4);
            // SAFETY: leave the child at once
            unsafe { libc::_exit(code) };
        }
        let mut status = 0;
        // SAFETY: waits for the child just forked
        unsafe { libc::waitpid(pid, &mut status, 0) };
        assert!(libc::WIFEXITED(status));
        assert_eq!(libc::WEXITSTATUS(status), 0, "child exit code");
    }
}
