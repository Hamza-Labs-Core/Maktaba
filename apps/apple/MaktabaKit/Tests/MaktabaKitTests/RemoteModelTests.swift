import Foundation
import Testing
@testable import MaktabaKit

// The Remote's model (iPhone, iPad) against BrowseStub's home: nested in BrowseClient, whose
// suites run one at a time on the one stub.
extension BrowseClient {
    @Suite struct RemoteReads {
        init() { BrowseStub.reset() }

        private static func queues(pausedAll: Bool) -> String {
            #"{"paused_all":\#(pausedAll),"issues_count":0,"stages":[],"queues":[]}"#
        }

        /// Pause all tapped while the home is read again (the 15 s read) is kept: that read
        /// was answered before the pause was saved, so it still says "not paused" and must
        /// not put that back on screen while the home is paused.
        @MainActor @Test func aChangeWhileTheHomeIsReadAgainIsKept() async throws {
            let suite = "maktabakit.test.\(UUID().uuidString)"
            let defaults = try #require(UserDefaults(suiteName: suite))
            defer { defaults.removePersistentDomain(forName: suite) }
            BrowseStub.on("GET", "/api/queues", (200, Self.queues(pausedAll: false)))
            BrowseStub.on("GET", "/api/issues", (200, #"{"groups":[]}"#))
            BrowseStub.on("GET", "/api/cluster", (200, #"{"home":{"name":"Home","accept_joins":false,"roles_supported":true},"nodes":[]}"#))
            BrowseStub.on("POST", "/api/queues/pause-all", (204, ""))
            let model = RemoteModel(session: AppSession(app: "test.remote", defaults: defaults),
                                    client: BrowseHome.client().0)
            await model.load()
            #expect(model.queue?.pausedAll == false)

            // read again; the queue is answered as the home has it now (not paused), then held
            let queue = BrowseStub.hold("GET", "/api/queues")
            let reading = Task { await model.load() }
            #expect(await queue.waitUntilAsked())
            BrowseStub.on("GET", "/api/queues", (200, Self.queues(pausedAll: true)))
            await model.setPausedAll(true)
            #expect(model.queue?.pausedAll == true, "the pause, read again after it was saved")
            queue.letGo()
            await reading.value

            #expect(model.queue?.pausedAll == true, "the read that began before the pause does not undo it")
        }
    }
}
