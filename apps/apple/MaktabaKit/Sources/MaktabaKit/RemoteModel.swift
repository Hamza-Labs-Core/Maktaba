import Foundation
import Observation

/// The home as the Remote sees it, for every screen: what needs attention, the processing
/// queue and its running jobs, the libraries, the machines, the updates, the processes.
/// Read every 15 s while the app is open and after each action. Each action is one the home checks itself (its admins only): the app hides
/// nothing as a guard. The Remote app (iPhone, iPad) shows it.
@MainActor @Observable
public final class RemoteModel {
    public let session: AppSession
    public let client: APIClient

    public private(set) var attention: [FailureGroup] = []
    public private(set) var queue: QueueSnapshot?
    public private(set) var libraries: [Library] = []
    public private(set) var cluster: ClusterOverview?
    public private(set) var running: [RunningJob] = []
    public private(set) var updates: UpdatesOverview?
    /// The run the Updates screen shows (the one under way, else the last), with its steps.
    public private(set) var updateRun: UpdateRunView?
    public private(set) var processes: ProcessesDoc?
    /// Why the running work (updates, processes, running jobs) could not be read, by part:
    /// an older home may not have one; the other screens are not held up by it.
    public private(set) var statusProblems: [StatusPart: String] = [:]
    public private(set) var loaded = false
    /// Why the last read or action did not work (nil once one works again).
    public var problem: String?
    /// What is under way: a group, a stage, a library, a machine, or "all".
    public private(set) var busy: Set<String> = []
    /// Libraries whose scan the home took, for a moment's "Scan started".
    public private(set) var scanned: Set<String> = []
    /// Bumped as each action starts and once its save is answered. A read that began before
    /// then was answered before the save, so it is dropped: applied, it would put the old
    /// state back on screen (a pause, a machine's join) while the home has the new one.
    private var actions = 0

    public init(session: AppSession, client: APIClient) {
        self.session = session
        self.client = client
    }

    public var retryable: [FailureGroup] { attention.filter { $0.action == "retry" } }
    public var asking: [Machine] { cluster?.nodes.filter { $0.group == .asking } ?? [] }
    public var attentionCount: Int { attention.reduce(0) { $0 + $1.count } }

    // MARK: reading

    public func load() async {
        let seen = actions
        async let a = read { try await self.client.needsAttention() }
        async let q = read { try await self.client.queues() }
        async let l = read { try await self.client.libraries() }
        async let c = read { try await self.client.cluster() }
        let (attention, queue, libraries, cluster) = await (a, q, l, c)
        // an action since the read began: it was answered before the save, and the action
        // reads again itself (the running work below is read now, after it)
        guard actions == seen else {
            await loadStatus()
            return
        }
        if let attention { self.attention = attention }
        if let queue { self.queue = queue }
        if let libraries { self.libraries = libraries }
        if let cluster { self.cluster = cluster }
        if attention != nil, queue != nil, libraries != nil, cluster != nil { problem = nil }
        await loadStatus()
        loaded = true
    }

    /// The running work: updates (and the run they show), processes, running jobs.
    private func loadStatus() async {
        async let u = readPart(.updates) { try await self.client.updates() }
        async let p = readPart(.processes) { try await self.client.processes() }
        async let r = readPart(.running) { try await self.client.runningJobs() }
        let (updates, processes, running) = await (u, p, r)
        if let updates {
            self.updates = updates
            if let shown = updates.shownRun {
                if let view = await readPart(.updateRun, { try await self.client.updateRun(shown.id) }) {
                    updateRun = view
                }
            } else {
                updateRun = nil
                statusProblems[.updateRun] = nil
            }
        }
        if let processes { self.processes = processes }
        if let running { self.running = running }
    }

    private func readPart<T>(_ part: StatusPart, _ f: @escaping () async throws -> T) async -> T? {
        do {
            let v = try await f()
            statusProblems[part] = nil
            return v
        } catch APIError.signedOut {
            session.signedOut()
            return nil
        } catch {
            guard !Log.isCancellation(error) else { return nil }
            Log.failure("remote status read", error)
            statusProblems[part] = HomeFailure(error).message()
            return nil
        }
    }

    /// Reads every 15 s until the task ends.
    public func poll() async {
        while !Task.isCancelled {
            await load()
            try? await Task.sleep(for: .seconds(15))
        }
    }

    private func read<T>(_ f: @escaping () async throws -> T) async -> T? {
        do {
            return try await f()
        } catch {
            fail(error)
            return nil
        }
    }

    private func fail(_ error: Error) {
        if case APIError.signedOut = error {
            session.signedOut()
            return
        }
        // a read cut short by leaving the screen is not a problem
        guard !Log.isCancellation(error) else { return }
        Log.failure("remote read or action", error)
        problem = HomeFailure(error).message()
    }

    // MARK: acting

    /// Runs one action under `key` (its row shows a spinner), then reads the home again.
    @discardableResult
    private func act(_ key: String, _ f: () async throws -> Void) async -> Bool {
        busy.insert(key)
        defer { busy.remove(key) }
        var ok = true
        actions += 1
        do {
            try await f()
            problem = nil
        } catch {
            fail(error)
            ok = false
        }
        actions += 1
        await load()
        return ok
    }

    public func retry(_ groups: [FailureGroup]) async {
        await act(groups.count == 1 ? groups[0].id : "all") {
            for g in groups { try await client.retry(stage: g.stage, cause: g.cause) }
        }
    }

    public func setPaused(_ row: QueueRow, _ paused: Bool) async {
        await act(row.id) { try await client.setPaused(stage: row.id, paused) }
    }

    public func setPausedAll(_ paused: Bool) async {
        await act("all") { try await client.setPausedAll(paused) }
    }

    public func failures(stage: String) async -> [FailureGroup]? {
        await read { try await self.client.failures(stage: stage) }
    }

    public func scan(_ library: Library) async {
        if await act(library.id, { try await client.scan(library: library.id) }) {
            scanned.insert(library.id)
            Task {
                try? await Task.sleep(for: .seconds(5))
                scanned.remove(library.id)
            }
        }
    }

    public func setAcceptJoins(_ open: Bool) async {
        await act("joins") { try await client.setAcceptJoins(open) }
    }

    /// Approves with the code the machine shows; nil when it worked, else why not (the
    /// sheet says it and stays open).
    public func approve(_ machine: Machine, code: String) async -> ApproveFailure? {
        busy.insert(machine.id)
        defer { busy.remove(machine.id) }
        var failure: ApproveFailure?
        actions += 1
        do {
            try await client.approve(machine: machine.id, code: code)
        } catch APIError.signedOut {
            session.signedOut()
        } catch {
            failure = ApproveFailure(error)
        }
        actions += 1
        await load()
        return failure
    }

    public func reject(_ machine: Machine) async {
        await act(machine.id) { try await client.reject(machine: machine.id) }
    }

    public func revoke(_ machine: Machine) async {
        await act(machine.id) { try await client.revoke(machine: machine.id) }
    }
}

/// A part of the running work the Remote reads on its own.
public enum StatusPart: String, Sendable {
    case updates, updateRun, processes, running
}
