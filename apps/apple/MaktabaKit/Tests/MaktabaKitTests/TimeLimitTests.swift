import Foundation
import Testing
@testable import MaktabaKit

/// Story 13.16: the Mac Player starting again waits for the last position to reach the home,
/// but never past its limit, whatever the report does.
@Suite struct TimeLimitTests {
    /// Work that does not hear cancellation, as `await task.value` on a report to a home that
    /// does not answer.
    private func stuck(_ seconds: Double) -> Task<Void, Never> {
        Task.detached { await withCheckedContinuation { c in DispatchQueue.global().asyncAfter(deadline: .now() + seconds) { c.resume() } } }
    }

    @Test func aReportThatDoesNotAnswerIsNotWaitedForPastTheLimit() async {
        let report = stuck(20)
        let start = ContinuousClock.now
        let ended = await TimeLimit.wait(.milliseconds(300)) { await report.value }
        let took = ContinuousClock.now - start
        #expect(!ended)
        #expect(took >= .milliseconds(300), "\(took)")
        #expect(took < .seconds(10), "\(took): the report (20 s) was waited for")
    }

    @Test func aReportThatIsSentEndsTheWaitEarly() async {
        let report = stuck(0.05)
        let start = ContinuousClock.now
        let ended = await TimeLimit.wait(.seconds(60)) { await report.value }
        #expect(ended)
        #expect(ContinuousClock.now - start < .seconds(30))
    }

    /// Both ends at about the same moment, many times: one answer each, nothing left waiting.
    @Test func theEndAndTheLimitTogetherGiveOneAnswer() async {
        await withTaskGroup(of: Void.self) { group in
            for _ in 0 ..< 200 {
                group.addTask { await TimeLimit.wait(.milliseconds(5)) { try? await Task.sleep(for: .milliseconds(5)) } }
            }
        }
    }

    /// The waiting task cancelled: the wait is over, not held to its limit.
    @Test func aCancelledWaitEnds() async {
        let report = stuck(20)
        let start = ContinuousClock.now
        let waiting = Task { await TimeLimit.wait(.seconds(60)) { await report.value } }
        waiting.cancel()
        #expect(await waiting.value == false)
        #expect(ContinuousClock.now - start < .seconds(10))
    }
}
