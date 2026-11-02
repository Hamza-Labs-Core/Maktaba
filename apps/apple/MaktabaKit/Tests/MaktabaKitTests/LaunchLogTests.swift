import Foundation
import Testing
@testable import MaktabaKit

/// Every launch's first log lines (`Log.launched`, called by PlayerApp and RemoteApp): the
/// Player UI jobs keep the app's log and tools/apple/check-app-log.py fails one that lacks
/// them, so their words are the check's: an info line "launched <bundle id> …" and a debug
/// line "launch arguments: N".
@Suite struct LaunchLog {
    @Test func theInfoLineNamesTheAppAndItsVersion() {
        let lines = Log.launchLines(subsystem: "com.hamzalabs.maktaba.player",
                                    info: ["CFBundleShortVersionString": "1.4.0", "CFBundleVersion": "77"],
                                    arguments: ["/path/Maktaba Player", "-maktaba-session-reset", "-AppleLanguages", "(ar)"])
        #expect(lines.info == "launched com.hamzalabs.maktaba.player 1.4.0 (77)")
        #expect(lines.debug == "launch arguments: 3")
    }

    @Test func linesTheCheckReads() throws {
        let lines = Log.launchLines(subsystem: "com.hamzalabs.maktaba.player.tablet", info: [:], arguments: ["/path/app"])
        #expect(lines.info == "launched com.hamzalabs.maktaba.player.tablet - (-)")
        #expect(lines.debug == "launch arguments: 0")
        // check-app-log.py's LAUNCHED and ARGUMENTS
        #expect(try Regex(#"^launched com\.hamzalabs\.maktaba\S* "#).firstMatch(in: lines.info) != nil)
        #expect(try Regex(#"^launch arguments: \d+$"#).wholeMatch(in: lines.debug) != nil)
    }

    @Test func noArgumentsAtAllIsZero() {
        #expect(Log.launchLines(subsystem: "x", info: [:], arguments: []).debug == "launch arguments: 0")
    }

    /// Logging them does not trap, whatever the bundle (the test runner's has no version).
    @Test func launchedLogs() {
        Log.launched(bundle: Bundle(for: BundleMarker.self))
        Log.launched()
    }
}

private final class BundleMarker {}
