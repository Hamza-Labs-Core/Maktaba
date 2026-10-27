import MaktabaKit
import SwiftUI

/// The home as the Remote sees it, for every screen: what needs attention, the processing
/// queue, the libraries, the machines. Read every 15 s while the app is open and after each
/// action. Each action is one the home checks itself (its admins only): the app hides
/// nothing as a guard.
@MainActor @Observable
final class RemoteModel {
    let session: AppSession
    let client: APIClient

    private(set) var attention: [FailureGroup] = []
    private(set) var queue: QueueSnapshot?
    private(set) var libraries: [Library] = []
    private(set) var cluster: ClusterOverview?
    private(set) var loaded = false
    /// Why the last read or action did not work (nil once one works again).
    var problem: AppError?
    /// What is under way: a group, a stage, a library, a machine, or "all".
    private(set) var busy: Set<String> = []
    /// Libraries whose scan the home took, for a moment's "Scan started".
    private(set) var scanned: Set<String> = []

    init(session: AppSession, client: APIClient) {
        self.session = session
        self.client = client
    }

    var retryable: [FailureGroup] { attention.filter { $0.action == "retry" } }
    var asking: [Machine] { cluster?.nodes.filter { $0.group == .asking } ?? [] }
    var attentionCount: Int { attention.reduce(0) { $0 + $1.count } }

    // MARK: reading

    func load() async {
        async let a = read { try await self.client.needsAttention() }
        async let q = read { try await self.client.queues() }
        async let l = read { try await self.client.libraries() }
        async let c = read { try await self.client.cluster() }
        let (attention, queue, libraries, cluster) = await (a, q, l, c)
        if let attention { self.attention = attention }
        if let queue { self.queue = queue }
        if let libraries { self.libraries = libraries }
        if let cluster { self.cluster = cluster }
        if attention != nil, queue != nil, libraries != nil, cluster != nil { problem = nil }
        loaded = true
    }

    /// Reads every 15 s until the task ends.
    func poll() async {
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
        let e = AppError(error)
        guard !e.isCancellation else { return }
        Log.failure("remote read or action", error)
        problem = e
    }

    // MARK: acting

    /// Runs one action under `key` (its row shows a spinner), then reads the home again.
    @discardableResult
    private func act(_ key: String, _ f: () async throws -> Void) async -> Bool {
        busy.insert(key)
        defer { busy.remove(key) }
        var ok = true
        do {
            try await f()
            problem = nil
        } catch {
            fail(error)
            ok = false
        }
        await load()
        return ok
    }

    func retry(_ groups: [FailureGroup]) async {
        await act(groups.count == 1 ? groups[0].id : "all") {
            for g in groups { try await client.retry(stage: g.stage, cause: g.cause) }
        }
    }

    func setPaused(_ row: QueueRow, _ paused: Bool) async {
        await act(row.id) { try await client.setPaused(stage: row.id, paused) }
    }

    func setPausedAll(_ paused: Bool) async {
        await act("all") { try await client.setPausedAll(paused) }
    }

    func failures(stage: String) async -> [FailureGroup]? {
        await read { try await self.client.failures(stage: stage) }
    }

    func scan(_ library: Library) async {
        if await act(library.id, { try await client.scan(library: library.id) }) {
            scanned.insert(library.id)
            Task {
                try? await Task.sleep(for: .seconds(5))
                scanned.remove(library.id)
            }
        }
    }

    func setAcceptJoins(_ open: Bool) async {
        await act("joins") { try await client.setAcceptJoins(open) }
    }

    /// Approves with the code the machine shows; nil when it worked, else why not (the
    /// sheet says it and stays open).
    func approve(_ machine: Machine, code: String) async -> ApproveFailure? {
        busy.insert(machine.id)
        defer { busy.remove(machine.id) }
        var failure: ApproveFailure?
        do {
            try await client.approve(machine: machine.id, code: code)
        } catch APIError.signedOut {
            session.signedOut()
        } catch {
            failure = ApproveFailure(error)
        }
        await load()
        return failure
    }

    func reject(_ machine: Machine) async {
        await act(machine.id) { try await client.reject(machine: machine.id) }
    }

    func revoke(_ machine: Machine) async {
        await act(machine.id) { try await client.revoke(machine: machine.id) }
    }
}
