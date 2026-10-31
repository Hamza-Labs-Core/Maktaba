import Foundation
import Testing
@testable import MaktabaKit

// The running work Maktaba Remote shows (its updates, processes and running jobs), read
// from JSON shaped as the home's handlers write it, with states and fields a newer home
// may add.

@Suite struct UpdatesScreen {
    static let overview = #"""
    {"current_version":"0.3.40","watermark":"0.3.40",
     "policy":{"auto_install":true,"channel":"stable","window":"3:00-05:00","window_overrun_minutes":30,
               "min_release_age_hours":24,"pinned_version":"","deferred_until":null,"skipped_versions":[],
               "node_failure_policy":"continue","keep_versions":2},
     "auto_install_available":true,
     "plan":{"target":{"version":"0.3.43","tag":"v0.3.43","channel":"stable","verify_state":"verified"},
             "newest":{"version":"0.3.44","tag":"v0.3.44","channel":"stable","verify_state":"verified"},
             "held":false,"stepping_stone":true,"auto_install":true,"due":false,
             "next_run_at":"2026-11-01T03:00:00Z","eligible_at":"2026-10-30T12:00:00.5Z","waiting":"outside_window"},
     "last_run":{"id":"r9","version":"0.3.40","from_version":"0.3.39","trigger":"auto","state":"done_with_failures",
                 "step":"commit","started_at":"2026-10-30T03:00:00Z","finished_at":"2026-10-30T03:20:00Z",
                 "error":{"code":"stage_failed","step":"stage","attempts":3}},
     "catalog":[
       {"version":"0.3.42","tag":"v0.3.42","channel":"stable","verify_state":"verified","blocked_reason":"rolled_back"},
       {"version":"0.3.39","tag":"v0.3.39","channel":"stable","verify_state":"verified","blocked_reason":"attempts"},
       {"version":"0.3.45","tag":"v0.3.45","channel":"stable","verify_state":"failed","blocked_reason":"verify"},
       {"version":"0.3.46","tag":"v0.3.46","channel":"stable","verify_state":"verified","blocked_reason":"rehearsal",
        "withdrawn_at":"2026-10-29T00:00:00Z"},
       {"version":"0.3.43","tag":"v0.3.43","channel":"stable","verify_state":"verified"},
       {"version":"0.3.44","tag":"v0.3.44","channel":"stable","verify_state":"verified","held_at":"2026-10-30T00:00:00Z"},
       {"tag":"broken"}],
     "last_check_error":"github: 403 rate limited","check_stale":true,"time_zone":"Asia/Amman",
     "machines":[
       {"id":"home","name":"Mac mini","role":"home","version":"0.3.40","state":"up_to_date","platform":"darwin/arm64",
        "last_update":{"id":"a1","at":"2026-10-30T03:00:00Z","ended_at":"2026-10-30T03:10:00Z","source":"run","to":"0.3.40","outcome":"installed"}},
       {"id":"n1","name":"Basement PC","role":"node","version":"0.3.39","state":"needs_a_reboot","target":"0.3.40",
        "home_db":{"ok":false,"code":"no_hba","detail":"pg_hba.conf rejects 192.168.1.20","fails":4}},
       {"id":"n2","name":"Attic","role":"node","version":"0.3.40","state":"up_to_date","home_db":{"ok":true}}],
     "stuck":{"version":"0.3.43","code":"tv_busy","windows":3,"since":"2026-10-27T03:00:00Z"},
     "tv_held":{"run_id":"r9","version":"0.3.40","outcome":"held","number":12,"held_on":"0.3.39","failed":["slot.health"],
                "state":"held","checks":[{"check":"slot.health","channel_id":"c1","result":"fail","value":"the staging slot runs another release"}]},
     "a_field_from_a_newer_home":{"x":1}}
    """#

    static let run = #"""
    {"id":"r10","version":"0.3.43","from_version":"0.3.40","trigger":"manual","state":"rolling_out","step":"watch",
     "started_at":"2026-10-31T03:00:00Z","install_kind":"appbundle",
     "detail":{"waiting":"tv_busy","tv":{"outcome":"held","held_on":"0.3.40","failed":["no_change"]},
               "waiting_home":[{"node_id":"n9","name":"Laptop","version":"0.3.40"}]},
     "steps":[
       {"seq":1,"host":"home","step":"stage","state":"done","started_at":"2026-10-31T03:00:00Z","detail":{"detail":"downloaded 120 MB"}},
       {"seq":2,"host":"home","step":"teleport","state":"warping","started_at":"2026-10-31T03:05:00Z","detail":"plain words"},
       {"seq":"three","host":"home"}],
     "targets":[
       {"run_id":"r10","node_id":"n1","name":"Basement PC","ord":1,"platform":"windows/amd64","kind":"appbundle","state":"app_staged",
        "from_version":"0.3.40","to_version":"0.3.43","attempts":1},
       {"run_id":"r10","node_id":"n2","name":"Attic","ord":2,"platform":"linux/amd64","kind":"runtime","state":"needs_person",
        "from_version":"0.3.40","to_version":"0.3.43","attempts":2,"error":{"code":"old_launcher","detail":"run `maktaba-install` once"}},
       {"run_id":"r10","node_id":"n3","name":"Garage","ord":3,"state":"quantum_superposition","attempts":0,
        "error":{"code":"brand_new_code"}}]}
    """#

    static func decode<T: Decodable>(_ json: String) throws -> T {
        try JSONDecoder.home.decode(T.self, from: Data(json.utf8))
    }

    @Test func readsThePlanAndWhatHoldsIt() throws {
        let o: UpdatesOverview = try Self.decode(Self.overview)
        #expect(o.currentVersion == "0.3.40" && o.autoInstall && o.window == "3:00-05:00")
        #expect(o.plan.target?.version == "0.3.43" && o.plan.newest?.version == "0.3.44")
        #expect(o.plan.steppingStone && !o.plan.held && o.plan.waiting == "outside_window")
        #expect(o.plan.nextRunAt == Date(timeIntervalSince1970: 1793502000))
        #expect(o.checkStale && o.lastCheckError == "github: 403 rate limited")
        #expect(o.stuck?.sentence == "0.3.43 has not installed in 3 windows: Someone watched TV until the quiet window ended")
        // the release without a version is left out, the rest kept
        #expect(o.catalog.count == 6)
        #expect(o.activeRun == nil && o.shownRun?.id == "r9")
        #expect(o.lastRun?.errorText == "Download and verify: failed")
        #expect(o.lastRun?.error["attempts"] == "3")
    }

    @Test func saysWhenTheNextUpdateInstalls() throws {
        let o: UpdatesOverview = try Self.decode(Self.overview)
        var cal = Calendar(identifier: .gregorian)
        cal.timeZone = TimeZone(identifier: "UTC")!
        let now = Date(timeIntervalSince1970: 1793502000 - 9 * 3600 - 15 * 60)
        #expect(o.next(now: now, calendar: cal) == "0.3.43 installs tomorrow between 03:00 and 05:00 (in 9 hours 15 minutes).")
    }

    @Test(arguments: [
        ("outside_window", "Version 1.0.0 installs in the next quiet window."),
        ("release_age", "Version 1.0.0 installs once it is old enough."),
        ("a_new_reason", "Version 1.0.0 is available: a new reason."),
    ])
    func saysWhyThePlanWaits(waiting: String, want: String) {
        #expect(UpdateWords.planWaiting(waiting, version: "1.0.0") == want)
    }

    /// As the web: only releases newer than what runs, and none a newer good release passed by.
    @Test func showsOnlyTheBlockedReleasesStillToCome() throws {
        let o: UpdatesOverview = try Self.decode(Self.overview)
        // 0.3.39: older than what runs; 0.3.42: 0.3.43 passed it by; 0.3.46: withdrawn;
        // 0.3.45: newer than every good release, still to come
        #expect(o.blockedReleases.map(\.version) == ["0.3.45"])
        #expect(UpdateWords.blocked("verify") == "its signature or identity was refused")
        #expect(UpdateWords.blocked("cosmic_rays") == "cosmic rays")
    }

    @Test func noBlockedReleaseWhenANewerOneIsNot() throws {
        let json = #"""
        {"current_version":"0.3.40","watermark":"","plan":{},"check_stale":false,
         "catalog":[{"version":"0.3.41","blocked_reason":"attempts"},{"version":"0.3.42"}]}
        """#
        let o: UpdatesOverview = try Self.decode(json)
        #expect(o.blockedReleases.isEmpty)
        let alone: UpdatesOverview = try Self.decode(json.replacingOccurrences(of: #",{"version":"0.3.42"}"#, with: ""))
        #expect(alone.blockedReleases.map(\.version) == ["0.3.41"])
    }

    @Test func readsTheMachinesAndTheirReachToTheDatabase() throws {
        let o: UpdatesOverview = try Self.decode(Self.overview)
        #expect(o.machines.map(\.name) == ["Mac mini", "Basement PC", "Attic"])
        let home = o.machines[0]
        #expect(home.role == "home" && home.lastUpdate?.to == "0.3.40" && home.lastUpdate?.how == "an update run")
        let pc = o.machines[1]
        #expect(pc.target == "0.3.40" && UpdateWords.state(pc.state) == "needs a reboot")
        #expect(pc.homeDB?.sentence == "This machine can't reach the home's database: the home's database does not let this machine in (no rule for its address)")
        #expect(o.machines[2].homeDB?.ok == true)
    }

    @Test func saysWhyTheTVIsHeld() throws {
        let o: UpdatesOverview = try Self.decode(Self.overview)
        let held = try #require(o.tvHeld)
        #expect(held.runID == "r9" && held.tv.held && held.tv.number == 12)
        #expect(held.summary == "The TV stays on 0.3.39: check slot.health failed: the staging slot runs another release")
        #expect(held.tv.title(release: "0.3.40") == "Installed. TV held on 0.3.39: its check failed")
    }

    @Test func readsARunWithItsStepsAndMachines() throws {
        let v: UpdateRunView = try Self.decode(Self.run)
        #expect(v.run.id == "r10" && v.run.state == "rolling_out" && !v.run.ended)
        #expect(UpdateWords.state(v.run.state) == "updating the machines")
        #expect(v.waiting.map(UpdateWords.waiting) == "Waiting until nobody watches TV.")
        #expect(v.installKind == "appbundle" && v.waitingHome.map(\.name) == ["Laptop"])
        #expect(v.tv?.failedChecks.map(\.check) == ["no_change"])
        // a step whose seq is not a number still shows; a newer step and state show raw
        #expect(v.steps.count == 3)
        #expect(v.steps[0].detail == "downloaded 120 MB" && UpdateWords.step(v.steps[0].step) == "Download and verify")
        #expect(v.steps[1].detail == "plain words" && UpdateWords.step("teleport") == "teleport")
        #expect(UpdateWords.state("warping") == "warping")
        #expect(v.targets.map(\.state) == ["app_staged", "needs_person", "quantum_superposition"])
        #expect(UpdateWords.state("app_staged") == "ready: installs at its turn")
        #expect(UpdateWords.state("quantum_superposition") == "quantum superposition")
        #expect(v.targets[1].errorText == "Its launcher is older than automatic updates: update it once by hand")
        #expect(v.targets[1].error["detail"] == "run `maktaba-install` once")
        #expect(v.targets[2].errorText == "Stopped at: brand_new_code")
    }

    /// An older home leaves most fields out: the screen still reads.
    @Test func readsAnOlderHomesSparseAnswer() throws {
        let o: UpdatesOverview = try Self.decode(#"{"current_version":"0.2.0","plan":null,"catalog":null,"run":{"id":"r1","state":7}}"#)
        #expect(o.plan.target == nil && o.catalog.isEmpty && o.machines.isEmpty)
        #expect(o.run?.state == "" && o.activeRun?.id == "r1")
    }

    @Test(arguments: [
        ("0.3.41", "0.3.40", 1), ("0.3.40", "0.3.40", 0), ("1.0.0-rc.1", "1.0.0", -1),
        ("1.0.0-rc.2", "1.0.0-rc.10", -1), ("0.3.40-3-gabc1234", "0.3.40", 1),
        ("0.3.40-3-gabc1234-dirty", "0.3.40-3-gabc1234", 1), ("v0.10.0", "0.9.9", 1),
        ("1.0.0-alpha", "1.0.0-1", 1), ("nonsense", "0.1.0", 0),
    ])
    func ordersVersionsAsTheServer(a: String, b: String, want: Int) {
        #expect(ReleaseVersion.compare(a, b) == want)
        if want != 0 { #expect(ReleaseVersion.compare(b, a) == -want) }
    }
}

@Suite struct ProcessesScreen {
    static let doc = #"""
    {"host":"mac-mini","started_at":"2026-10-31T08:00:00Z","available":true,
     "policy":{"restart":"always","backoff_initial_sec":1,"backoff_max_sec":60,"max_restarts":5},
     "processes":[
       {"role":"api","wanted":false,"state":"running","pid":101,"restarts":0,"crash_loop":false,"uptime_sec":7265.4},
       {"role":"tv-slot","instance":"blue","wanted":false,"state":"running","pid":102,"restarts":1,"crash_loop":false,"uptime_sec":30,
        "last_exit":{"at":"2026-10-31T09:00:00Z","code":-1,"signal":"SIGKILL","reason":"crash","uptime_sec":12}},
       {"role":"tv-slot","instance":"amber","wanted":true,"state":"backoff","restarts":4,"crash_loop":true,"uptime_sec":0,
        "next_restart_at":"2026-10-31T10:05:00Z","detail":"port 9104 is held by pid 77 (maktaba-streaming)",
        "detail_code":"port_taken","detail_params":{"port":9104,"holder_pid":77},
        "last_exit":{"code":-1,"reason":"start_failed","error":"listen tcp :9104: bind: address already in use","uptime_sec":0}},
       {"role":"pipeline","state":"levitating","restarts":0,"crash_loop":false,"last_probe":{"ok":false,"consecutive_failures":3}},
       {"role":"ollama","wanted":true,"state":"stopped","restarts":"lots"},
       "not a process"],
     "events":[
       {"seq":40,"at":"2026-10-31T09:00:00Z","role":"tv-slot","instance":"blue","kind":"exited","code":-1,"signal":"SIGKILL","reason":"crash"},
       {"seq":41,"at":"2026-10-31T09:00:01Z","role":"tv-slot","instance":"blue","kind":"restarting","delay_sec":1},
       {"seq":42,"at":"2026-10-31T09:10:00Z","role":"tv-slot","instance":"amber","kind":"failed","message":"crash loop"},
       {"seq":43,"at":"2026-10-31T09:20:00Z","role":"api","kind":"teleported"}],
     "ffmpeg":{"ffmpeg":"/usr/bin/ffmpeg","ffprobe":"/usr/bin/ffprobe","source":"path","ok":true}}
    """#

    @Test func readsEveryProcessByRoleAndInstance() throws {
        let d = try JSONDecoder.home.decode(ProcessesDoc.self, from: Data(Self.doc.utf8))
        #expect(d.available && d.host == "mac-mini")
        #expect(d.processes.map(\.id) == ["api", "tv-slot/blue", "tv-slot/amber", "pipeline", "ollama"])
        #expect(d.processes.map(\.name) == ["API", "TV slot blue", "TV slot amber", "Processing pipeline", "ollama"])
        let amber = d.processes[2]
        #expect(amber.wanted && amber.crashLoop && amber.restarts == 4 && amber.stateText == "Restarting")
        #expect(amber.detailCode == "port_taken" && amber.detailText == "port 9104 is held by pid 77 (maktaba-streaming)")
        #expect(amber.lastExit?.sentence == "could not start" && amber.lastExit?.failed == true)
        #expect(d.processes[1].lastExit?.sentence == "killed by SIGKILL")
        // a state the app does not know shows raw; a field of the wrong shape reads as absent
        #expect(d.processes[3].stateText == "levitating" && d.processes[3].lastProbe?.failures == 3)
        #expect(d.processes[4].restarts == 0 && d.processes[4].stateText == "Stopped")
    }

    @Test func listsTheRecentEventsNewestFirst() throws {
        let d = try JSONDecoder.home.decode(ProcessesDoc.self, from: Data(Self.doc.utf8))
        let recent = d.recentEvents(3)
        #expect(recent.map(\.seq) == [43, 42, 41])
        #expect(recent[0].sentence == "teleported" && recent[1].sentence == "crash loop: retried slowly" && recent[1].isFailure)
        #expect(d.events[0].processName == "TV slot blue" && d.events[0].sentence == "killed by SIGKILL")
    }

    @Test func saysWhenTheSupervisorIsNotThere() throws {
        let json = #"{"available":false,"reason":"supervisor unreachable: dial tcp 127.0.0.1:9103: connection refused","processes":[],"events":[]}"#
        let d = try JSONDecoder.home.decode(ProcessesDoc.self, from: Data(json.utf8))
        #expect(!d.available && d.processes.isEmpty && d.reason?.hasPrefix("supervisor unreachable") == true)
    }
}

@Suite struct RunningJobsScreen {
    static let running = #"""
    {"host":"","jobs":[
      {"id":7,"stage":"transcribe","state":"running","subject_kind":"video","subject_id":"v1","subject_label":"Lecture 3",
       "library_id":"l1","priority":0,"attempts":1,"max_attempts":3,"outcome":null,"error_cause":null,
       "created_at":"2026-10-31T09:00:00Z","started_at":"2026-10-31T09:55:00.25Z","finished_at":null,
       "progress":{"step":"whisper","steps":["load","whisper"],"done_units":30,"total_units":120},"claimed_by":"w1"},
      {"id":8,"stage":"probe","state":"claimed","subject_kind":"video","subject_id":"v2","subject_label":"",
       "attempts":0,"max_attempts":3,"created_at":"2026-10-31T10:00:00Z","started_at":null,"progress":null},
      {"id":"nine","stage":"x"},
      {"id":10,"stage":"teleport","state":"warping","subject_label":"Something","started_at":"not a time"}]}
    """#

    @Test func readsTheJobsAndHowLongEachHasRun() throws {
        let r = try JSONDecoder.home.decode(RunningJobs.self, from: Data(Self.running.utf8))
        #expect(r.jobs.map(\.id) == [7, 8, 10])
        let t = r.jobs[0]
        #expect(t.stage == "transcribe" && t.subjectLabel == "Lecture 3" && t.progressStep == "whisper" && t.fraction == 0.25)
        let started = try #require(t.startedAt)
        #expect(t.runningFor(now: started.addingTimeInterval(3900)) == 3900)
        #expect(runningText(3900) == "running for 1h 5m")
        // no label: its subject id; not started yet: no time
        #expect(r.jobs[1].subjectLabel == "v2" && r.jobs[1].runningFor() == nil && r.jobs[1].fraction == nil)
        #expect(r.jobs[2].state == "warping" && r.jobs[2].startedAt == nil)
    }

    @Test(arguments: [(40.0, "40s"), (720, "12m"), (3900, "1h 5m"), (183600, "2d 3h")])
    func saysADuration(secs: Double, want: String) {
        #expect(shortDuration(secs) == want)
    }
}
