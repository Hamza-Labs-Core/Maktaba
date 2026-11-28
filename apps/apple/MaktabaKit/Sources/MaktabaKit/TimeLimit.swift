import Foundation

/// A wait with a real limit. A task group cannot give one: it waits for every child before it
/// returns, so a child that does not hear cancellation (`await task.value`) holds it past any
/// limit raced against it.
public enum TimeLimit {
    /// Waits until `done` has returned, `limit` at most (or until the waiting task is
    /// cancelled), and says whether it did. `done` is then cancelled and left to end in its
    /// own time.
    @discardableResult
    public static func wait(_ limit: Duration, until done: @escaping @Sendable () async -> Void) async -> Bool {
        // the first answer counts; a stream takes a later one, and ends with the waiting task
        let (answers, answer) = AsyncStream.makeStream(of: Bool.self, bufferingPolicy: .bufferingOldest(1))
        let work = Task {
            await done()
            answer.yield(true)
        }
        let timer = Task {
            try? await Task.sleep(for: limit)
            answer.yield(false)
        }
        defer {
            work.cancel()
            timer.cancel()
            answer.finish()
        }
        for await ended in answers { return ended }
        return false
    }
}
