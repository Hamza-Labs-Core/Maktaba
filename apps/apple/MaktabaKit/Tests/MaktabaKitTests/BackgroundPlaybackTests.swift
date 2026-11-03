import AVFoundation
import Foundation
import Testing
@testable import MaktabaKit

// Picture in Picture and the sound in the background on iPhone and iPad: both need the audio
// background mode in the app's own Info.plist (a build setting named
// INFOPLIST_KEY_UIBackgroundModes is not one Xcode knows, so the shipped app had none: the PiP
// button stayed off and leaving the app cut the video), and a player that keeps playing when
// the app leaves without Picture in Picture.
@Suite struct BackgroundPlayback {
    /// apps/apple, from this file.
    private var appleDir: URL {
        URL(fileURLWithPath: #filePath).deletingLastPathComponent() // MaktabaKitTests
            .deletingLastPathComponent().deletingLastPathComponent() // Tests, MaktabaKit
            .deletingLastPathComponent()
    }

    @Test func theAudioModeIsReadFromAnInfoPlist() {
        #expect(BackgroundAudio.isDeclared(in: ["UIBackgroundModes": ["audio"]]))
        #expect(BackgroundAudio.isDeclared(in: ["UIBackgroundModes": ["fetch", "audio"]]))
        #expect(!BackgroundAudio.isDeclared(in: ["UIBackgroundModes": ["fetch"]]))
        #expect(!BackgroundAudio.isDeclared(in: ["UIBackgroundModes": "audio"]), "an array, not a string")
        #expect(!BackgroundAudio.isDeclared(in: [:]))
        #expect(!BackgroundAudio.isDeclared(in: nil))
    }

    @Test func thePlayersInfoPlistsDeclareTheAudioMode() throws {
        for plist in ["Player/Info-iPhone.plist", "Player/Info-iPad.plist"] {
            let data = try Data(contentsOf: appleDir.appendingPathComponent(plist))
            let info = try PropertyListSerialization.propertyList(from: data, format: nil) as? [String: Any]
            #expect(BackgroundAudio.isDeclared(in: info), "\(plist): UIBackgroundModes has audio")
        }
    }

    /// The setting that never reached the app is not back: the mode lives in `info.properties`
    /// (the Info.plist), where Xcode reads it.
    @Test func theProjectDoesNotSetTheModeThroughABuildSetting() throws {
        for file in ["project.yml", "Maktaba.xcodeproj/project.pbxproj"] {
            let text = try String(contentsOf: appleDir.appendingPathComponent(file), encoding: .utf8)
            #expect(!text.contains("INFOPLIST_KEY_UIBackgroundModes"), "\(file)")
        }
    }

    @MainActor @Test func aPlayerKeepsPlayingWhenTheAppLeaves() {
        let player = AVPlayer()
        #expect(player.audiovisualBackgroundPlaybackPolicy == .automatic, "the default pauses a drawn video")
        player.keepPlayingInBackground()
        #expect(player.audiovisualBackgroundPlaybackPolicy == .continuesIfPossible)
    }
}
