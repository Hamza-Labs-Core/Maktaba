import Foundation

// The words Maktaba Remote shows for what a home reports about its running work: the
// web's English (web/src/i18n/en.json), so the apps and the web read the same. Generated
// from en.json's keys named beside each table; a value with no words here shows raw, its
// underscores as spaces (a newer home's value). The Arabic is the web's ar.json, in
// MaktabaKit's catalog under the same keys (`updates.state.done`); these tables are the
// English and the list of values that have words.

public enum StatusWords {
    /// A run's, a machine's or a target's state (`updates.state.*`)
    public static let states: [String: String] = [
        "aborted": "stopped; nothing changed",
        "ahead_of_home": "ahead of the home",
        "app_staged": "ready: installs at its turn",
        "apply_failed": "could not update",
        "applying": "updating",
        "awaiting_click": "downloading the update",
        "behind": "behind",
        "cancelled": "cancelled",
        "clicked": "ready: installs at its turn",
        "committed": "kept the new release",
        "converting": "becoming a full node",
        "done": "done",
        "done_needs_person": "done, machines need you",
        "done_with_failures": "done, with failures",
        "draining": "finishing running jobs",
        "failed": "failed",
        "failed_prepare": "could not prepare",
        "failed_rollback": "needs you: could not go back",
        "healthy": "updated",
        "held": "held",
        "home_applying": "the home updates",
        "home_committed": "the home is updated",
        "home_prepared": "the home is ready",
        "home_preparing": "the home prepares",
        "needs_person": "needs you",
        "offline": "offline",
        "ordered": "downloading the update",
        "pending": "waiting",
        "planned": "planned",
        "prepare_failed": "could not prepare",
        "prepared": "prepared",
        "preparing": "preparing",
        "restarting": "restarting",
        "rolled_back": "rolled back",
        "rolling_back": "going back",
        "rolling_out": "updating the machines",
        "running": "running",
        "skipped": "skipped",
        "superseded": "replaced by a newer release",
        "up_to_date": "up to date",
        "updating": "updating",
        "waiting_click": "downloading the update",
        "watching": "watching the new release",
        "worker_only": "worker only",
    ]

    /// A run's step (`updates.step.*`)
    public static let steps: [String: String] = [
        "ask": "Maktaba on this machine downloads and checks the release",
        "backup": "Back up the library database (it keeps serving)",
        "check": "Check for the update",
        "commit": "Keep",
        "download": "Download",
        "dump": "Save the library database",
        "install": "Install",
        "launcher": "Replace the launcher",
        "migrate": "Update the library database",
        "order": "Maktaba on this machine downloads and checks the release",
        "preflight": "Checks",
        "rehearse": "Rehearse the database changes",
        "relaunch": "Start again on the new release",
        "restart": "Restart",
        "restore": "Put the library database back",
        "rollback": "Roll back",
        "smoke": "Test run",
        "stage": "Download and verify",
        "start": "Start the new release",
        "switch": "Switch",
        "tv": "Move the TV to the new release",
        "venv": "Build the pipeline's Python environment",
        "verify": "Verify the signature",
        "watch": "Watch",
    ]

    /// An error code (`updates.code.*`)
    public static let codes: [String: String] = [
        "admin": "An admin rolled the update back",
        "admin_rollback": "An admin rolled it back",
        "app_did_not_install": "The desktop app did not install the update in time",
        "app_install_failed": "The desktop app could not install the update",
        "app_management": "macOS does not let Maktaba replace itself: it needs App Management permission, or the app's files are locked",
        "app_not_started": "The desktop app could not be started",
        "app_order_gone": "The desktop app's install order was withdrawn or replaced",
        "app_request_failed": "The desktop app could not be reached",
        "apply_failed": "It could not be installed; the previous release runs",
        "apply_refused": "The machine refused to install it",
        "asset_not_found": "The update's download was not found",
        "bad_job": "The update order was not valid",
        "boot_loop": "The new release kept stopping as it started: the previous one runs again",
        "cancelled": "An admin cancelled it",
        "check_failed": "The app could not check for the update",
        "checksums_changed": "The release's checksums changed after it was verified",
        "convert_refused": "The machine's worker refused to become a full node (the detail says why)",
        "convert_timeout": "The machine did not come back as a full node on the release in time (2 hours)",
        "database": "The library database could not be prepared for the update",
        "db_ahead": "The library database was changed by a newer Maktaba than this one",
        "db_dump_failed": "The library database could not be saved before the update, so nothing was changed",
        "db_migrate_failed": "The library database could not be brought up to date for the new release",
        "db_refused_older": "This release is older than the library database: install the newer release again",
        "db_restore_failed": "The library database could not be put back as it was before the update",
        "did_not_start": "The new release did not start",
        "did_not_stay_up": "The new release did not stay running",
        "disk_full": "There is no room on the disk to download or unpack the update",
        "disk_space": "Not enough free disk space",
        "download_failed": "The release could not be downloaded",
        "failed": "The update failed",
        "failed_prepare": "The machine could not get the release ready (its download, check or unpacking failed)",
        "failed_tv": "The TV could not move to the new release (its swap failed, or it failed on the air after it switched): the previous release runs again",
        "feed_invalid": "The release's update feed could not be read",
        "feed_not_found": "The release's update feed was not found",
        "held": "Held: a machine on the same platform failed",
        "home_not_prepared": "The home was not ready to install it",
        "home_timeout": "The home took too long to update",
        "install_failed": "The release could not be installed",
        "installer_exit": "The system's package installer failed",
        "installer_not_finished": "The Windows installer did not finish after the app closed",
        "interrupted": "The update was interrupted; the previous release runs",
        "journal_lost": "The home lost track of this update",
        "keep_failed": "A copy of the running release could not be kept",
        "launcher_boot_loop": "The new release kept stopping as it started: the previous one runs again",
        "launcher_pending_timeout": "The new release did not confirm it was running in time: the previous one runs again",
        "launcher_protocol": "The release needs a newer launcher than this machine has",
        "launcher_refused": "The launcher refused the new release (its files did not check out): the previous one runs again",
        "local_build": "This app was built locally and can't update itself: install the release by hand once",
        "login_item_unsigned": "macOS would not start the server's login item: this copy of Maktaba is not code-signed",
        "needs_admin": "The desktop app cannot be replaced without an administrator's password: move it to a folder this user can change, or have an administrator install the update",
        "needs_person": "This machine can't update itself as it is installed: it needs a person",
        "no_app": "The desktop app is not where it was installed",
        "no_app_update": "The release has no desktop app update for this machine",
        "no_network": "The update could not be reached: this machine has no network connection, or the address could not be found",
        "no_previous": "There is no previous release to go back to",
        "no_python": "No Python 3.12 for the pipeline on this machine: install one, or name one in MAKTABA_PIPELINE_PYTHON",
        "no_release": "The release could not be found",
        "no_runtime": "This install has no current runtime",
        "no_trust_root": "This build cannot verify releases",
        "node_health_timeout": "The machine did not come back healthy in time",
        "not_self_managed": "This install cannot update itself yet",
        "not_writable": "The install folder cannot be written to",
        "old_launcher": "Its launcher is older than automatic updates: update it once by hand",
        "older_build": "an older build than the one running here (a release made before it, though numbered higher): passed by, nothing changed",
        "orchestrator_unavailable": "This server does not install updates by itself",
        "outside_window": "The quiet window ended before it could start",
        "payload_invalid": "The downloaded release is damaged",
        "pending_timeout": "The new release did not confirm it was running in time: the previous one runs again",
        "pg_major_change": "the new release bundles another major version of PostgreSQL, and this machine's library database cannot move to it by itself: nothing was changed",
        "prepare_failed": "It could not be prepared; nothing changed",
        "prepare_refused": "The machine refused to prepare it",
        "prepare_timeout": "The machine took too long to prepare it",
        "previous_app_not_restored": "The update failed and the app could not be put back in its folder",
        "python_store_stub": "The only Python on this machine is the Microsoft Store's alias, which runs no Python",
        "quarantined": "macOS still marks Maktaba as downloaded from the internet and unchecked: open it once from Applications (Control-click, then Open) and try again",
        "refused": "The home refused the update",
        "rehearsal_failed": "The database changes failed when rehearsed on a copy; nothing changed",
        "relaunch_failed": "The app did not come back on the new release",
        "release_incomplete": "The release lacks a file this machine needs",
        "replaced_by_newer": "A newer release was installed on this machine meanwhile",
        "restarts": "The new release restarted too often: the previous one runs again",
        "rollback_failed": "It could not go back to the previous release",
        "rollback_loop": "Going back to the previous release did not take",
        "rolled_back": "The new release did not hold: the previous one runs again",
        "server_stop_failed": "The server could not be stopped for the install",
        "signature_mismatch": "The download's signature does not match this app's key, so it was not installed",
        "smoke_failed": "The new release failed its test run",
        "smoke_missing": "The new release's program is missing",
        "smoke_version": "The new release reports another version",
        "start_failed": "The new release did not start (the detail says why): the previous one runs again",
        "start_interrupted": "The new release's start never finished: the server or the machine stopped, or the launcher stopped it and went back",
        "start_timeout": "The new release took too long to start (its database step or its Python environment did not finish in time): the previous one runs again",
        "superseded": "A newer release took its place",
        "switch_failed": "The switch to the new release failed; the previous release runs",
        "switchover": "A switch of the home began while this update waited for Maktaba to download it: its orders were withdrawn, and the new home orders them again",
        "switchover_open": "A change of home is under way",
        "translocated": "macOS runs this copy of Maktaba from a temporary read-only place (it was opened from Downloads or the disk image): move Maktaba to Applications and open it from there",
        "trust_root": "The keys to verify releases could not be read",
        "turned_prerelease": "The release became a pre-release",
        "tv_busy": "Someone watched TV until the quiet window ended",
        "tv_change_cancelled": "An admin cancelled the TV change of this update: the previous release runs again",
        "tv_change_pending": "The TV change waited behind another change awaiting approval until the quiet window ended",
        "tv_held": "The TV stayed on the previous version: its check failed",
        "tv_timeout": "The TV did not move to the new release in time: the previous release runs again",
        "venv_failed": "The pipeline's Python environment could not be built",
        "venv_interrupted": "Building the pipeline's Python environment was cut short: it goes on at the next start",
        "venv_network": "The pipeline's Python packages could not be downloaded: this machine cannot reach PyPI",
        "venv_python_unsupported": "This machine's Python is not one the release's pipeline packages are made for: install the Python the detail names, or name one in MAKTABA_PIPELINE_PYTHON",
        "verify_checksum": "A file of the release does not match its checksum",
        "verify_downgrade": "The release is older than the one installed",
        "verify_failed": "The release could not be verified",
        "verify_identity": "The release was signed by someone other than Maktaba's builds",
        "verify_manifest": "The release's manifest is not valid",
        "verify_replay": "An old signed release was offered as a new one",
        "verify_signature": "The release's signature is not valid",
        "verify_transparency_log": "The release's signature is not in the public transparency log",
        "verify_trust_root_expired": "This build's keys to verify releases have expired: update Maktaba once by hand",
        "verify_unhashed_dependencies": "The release has dependencies without checksums",
        "version_mismatch": "The downloaded release is not the version asked for",
        "watch_timeout": "The new release never settled (neither healthy nor failing for long enough): the previous one runs again",
        "window_over": "The quiet window ended before its turn",
        "withdrawn": "The release was withdrawn",
    ]

    /// Why a run waits now (`updates.waiting.*`)
    public static let waiting: [String: String] = [
        "needs_person": "The last try needs you (see its reason): the next window tries again, or Update now.",
        "outside_window": "Waiting for the quiet window.",
        "retry_backoff": "The last try failed: the next one in a few minutes.",
        "retry_next_window": "Tried enough in this window: the next window tries again.",
        "tv_busy": "Waiting until nobody watches TV.",
        "tv_swapping": "The restart waits for a TV change to finish switching channels (two minutes at most).",
    ]

    /// Why the planned release is not due ({version}) (`updates.plan.waiting.*`)
    public static let planWaitingTable: [String: String] = [
        "auto_install_off": "Version {version} is available; automatic install is off.",
        "automatic_installs_not_built": "Version {version} is available.",
        "deferred": "Version {version} is deferred.",
        "no_upgrade_path": "Version {version} cannot be installed from this version.",
        "outside_window": "Version {version} installs in the next quiet window.",
        "release_age": "Version {version} installs once it is old enough.",
    ]

    /// Why a release is blocked (`updates.blocked.reason.*`)
    public static let blockedReasons: [String: String] = [
        "attempts": "it failed in three windows",
        "failed_tv": "the TV could not move to it",
        "older_build": "an older build than the one running here: it is passed by",
        "rehearsal": "its migrations failed on a copy of the database",
        "rolled_back": "it was rolled back on the home",
        "verify": "its signature or identity was refused",
    ]

    /// Why a machine cannot reach the home's database (`machines.homeDb.code.*`)
    public static let homeDBCodes: [String: String] = [
        "auth_failed": "the home's database refused its password",
        "cert_mismatch": "its certificate is for another database role than its login's",
        "no_database": "the library database is missing on the home",
        "no_hba": "the home's database does not let this machine in (no rule for its address)",
        "other": "an error the details say",
        "refused": "the home's database refused the connection",
        "tls": "the secure connection to the home's database failed",
        "unreachable": "the home's database could not be reached over the network",
    ]

    /// A process's state (`health.processes.state.*`)
    public static let processStates: [String: String] = [
        "backoff": "Restarting",
        "exited": "Stopped",
        "failed": "Failed",
        "running": "Running",
        "starting": "Starting",
        "stopped": "Stopped",
        "unhealthy": "Not responding",
    ]

    /// A process's role (`health.processes.role.*`)
    public static let processRoles: [String: String] = [
        "api": "API",
        "pipeline": "Processing pipeline",
        "streaming": "Live TV and streaming",
        "tv-slot": "TV slot",
    ]

    /// A TV slot's colour, its instance (`tvchanges.slots.color.*`)
    public static let slotColors: [String: String] = [
        "amber": "amber",
        "blue": "blue",
        "green": "green",
    ]

    /// A process's name: its role's words, a TV slot with its colour ("TV slot blue").
    static func processName(role: String, instance: String?, locale: Locale = AppLanguage.locale) -> String {
        let base = words(processRoles, "health.processes.role.", role, locale)
        guard let instance else { return base }
        let i = role == "tv-slot" ? words(slotColors, "tvchanges.slots.color.", instance, locale) : instance
        return "\(base) \(i)"
    }
}

/// A value's words from `table`, whose catalog keys are `prefix` + the value, in `locale`'s
/// language; a value with no words shows as sent, its underscores as spaces.
func words(_ table: [String: String], _ prefix: String, _ value: String, _ locale: Locale = AppLanguage.locale) -> String {
    guard let en = table[value] else { return value.replacingOccurrences(of: "_", with: " ") }
    return kitWord(prefix + value, locale) ?? en
}

extension StatusWords {
    /// Each table with the web's key its values are under (en.json, ar.json and the catalog).
    public static let tables: [(prefix: String, words: [String: String])] = [
        ("updates.state.", states), ("updates.step.", steps), ("updates.code.", codes),
        ("updates.waiting.", waiting), ("updates.plan.waiting.", planWaitingTable),
        ("updates.blocked.reason.", blockedReasons), ("machines.homeDb.code.", homeDBCodes),
        ("health.processes.state.", processStates), ("health.processes.role.", processRoles),
        ("tvchanges.slots.color.", slotColors),
    ]
}

/// The sentences of the Updates screen, from the web's words, in the app's language (or
/// `locale`'s).
public enum UpdateWords {
    public static func state(_ s: String, locale: Locale = AppLanguage.locale) -> String {
        words(StatusWords.states, "updates.state.", s, locale)
    }

    public static func step(_ s: String, locale: Locale = AppLanguage.locale) -> String {
        words(StatusWords.steps, "updates.step.", s, locale)
    }

    public static func waiting(_ s: String, locale: Locale = AppLanguage.locale) -> String {
        words(StatusWords.waiting, "updates.waiting.", s, locale)
    }

    public static func blocked(_ reason: String, locale: Locale = AppLanguage.locale) -> String {
        words(StatusWords.blockedReasons, "updates.blocked.reason.", reason, locale)
    }

    /// An error code's words: its own, a check of the release's verification, a step's
    /// failure ("stage_failed": "Download and verify: failed"), else "Stopped at: <code>".
    public static func code(_ c: String, locale: Locale = AppLanguage.locale) -> String {
        known(c, locale) ?? kit("Stopped at: \(c)", locale)
    }

    /// code()'s words when the code has some (not "Stopped at:").
    private static func known(_ c: String, _ locale: Locale) -> String? {
        if StatusWords.codes[c] != nil { return words(StatusWords.codes, "updates.code.", c, locale) }
        if c.hasPrefix("verify_") { return words(StatusWords.codes, "updates.code.", "verify_failed", locale) }
        if c.hasSuffix("_failed") {
            let step = String(c.dropLast("_failed".count))
            if !step.isEmpty, step.allSatisfy({ $0.isASCII && $0.isLowercase }), StatusWords.steps[step] != nil {
                return "\(self.step(step, locale: locale)): \(state("failed", locale: locale))"
            }
        }
        return nil
    }

    /// A reason's words as code() has them, else the code with its underscores as spaces.
    public static func reason(_ c: String, locale: Locale = AppLanguage.locale) -> String {
        known(c, locale) ?? c.replacingOccurrences(of: "_", with: " ")
    }

    /// The planned release's wait in a sentence.
    public static func planWaiting(_ waiting: String, version: String, locale: Locale = AppLanguage.locale) -> String {
        guard StatusWords.planWaitingTable[waiting] != nil else {
            return kit("Version \(version) is available: \(waiting.replacingOccurrences(of: "_", with: " ")).", locale)
        }
        return words(StatusWords.planWaitingTable, "updates.plan.waiting.", waiting, locale)
            .replacingOccurrences(of: "{version}", with: version)
    }

    /// The plan's reason (up_to_date, no_upgrade_path, needs_approval) in a sentence.
    public static func planReason(_ reason: String, locale: Locale = AppLanguage.locale) -> String {
        switch reason {
        case "up_to_date": kit("Nothing to install: up to date.", locale)
        case "no_upgrade_path": kit("No release can be installed from this version.", locale)
        case "needs_approval": kit("The release waits for your approval.", locale)
        default: reason.replacingOccurrences(of: "_", with: " ")
        }
    }

    /// The quiet window's ends ("03:00", "05:00"), nil without a valid window.
    public static func windowEnds(_ w: String?) -> (String, String)? {
        guard let w, let m = w.wholeMatch(of: /\s*(\d{1,2}:\d{2})\s*-\s*(\d{1,2}:\d{2})\s*/) else { return nil }
        func pad(_ s: Substring) -> String { s.count < 5 ? "0" + s : String(s) }
        return (pad(m.1), pad(m.2))
    }

    /// today, tomorrow, else its weekday and date.
    public static func day(_ at: Date, now: Date, calendar: Calendar = .current, locale: Locale = AppLanguage.locale) -> String {
        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: now), to: calendar.startOfDay(for: at)).day ?? 0
        if days == 0 { return kit("today", locale) }
        if days == 1 { return kit("tomorrow", locale) }
        var style = Date.FormatStyle.dateTime.weekday(.wide).month(.abbreviated).day().locale(locale)
        style.calendar = calendar
        style.timeZone = calendar.timeZone
        return at.formatted(style)
    }

    /// "in 2 hours 15 minutes", or "now" when it is due.
    public static func span(until at: Date, now: Date, locale: Locale = AppLanguage.locale) -> String {
        let secs = at.timeIntervalSince(now)
        if secs <= 30 { return kit("now", locale) }
        let mins = Int((secs / 60).rounded())
        if mins < 1 { return kit("in less than a minute", locale) }
        let d = mins / 1440, h = (mins % 1440) / 60, m = mins % 60
        let days = kit("\(d) days", locale), hours = kit("\(h) hours", locale), minutes = kit("\(m) minutes", locale)
        if d > 0 { return h > 0 ? kit("in \(days) \(hours)", locale) : kit("in \(days)", locale) }
        if h > 0 { return m > 0 ? kit("in \(hours) \(minutes)", locale) : kit("in \(hours)", locale) }
        return kit("in \(minutes)", locale)
    }
}

