import Foundation
import Testing
@testable import MaktabaKit

// The live player's banner and mini-guide timing (`LiveOverlays`), on a fake clock: every
// instant is `t0` plus seconds.
struct LiveOverlaysTests {
    let t0 = ContinuousClock.now
    func at(_ s: Double) -> ContinuousClock.Instant { t0 + .milliseconds(Int(s * 1000)) }

    @Test func aQuickStartHidesTheBannerAfterItsTime() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(1))
        #expect(o.banner(at: at(6.9)))
        #expect(!o.banner(at: at(7)), "6 s after the tune, the later of tune and playing")
        #expect(o.nextChange(after: at(2)) == at(7))
    }

    /// The bug: a channel that took longer than the banner's time to start kept
    /// the banner up for good.
    @Test func aSlowStartStillHidesTheBanner() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        #expect(o.banner(at: at(30)), "it stays while nothing plays")
        #expect(o.nextChange(after: at(30)) == nil, "and nothing will hide it until it plays")
        o.playback(playing: true, at: at(40))
        #expect(o.banner(at: at(45)), "the full time from playing")
        #expect(!o.banner(at: at(46)))
        #expect(o.nextChange(after: at(40)) == at(46))
    }

    @Test func aRetuneShowsTheBannerAgain() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(1))
        #expect(!o.banner(at: at(20)))
        o.tuned(at: at(20))
        #expect(!o.playing, "a new channel starts over")
        #expect(o.banner(at: at(21)))
        o.playback(playing: true, at: at(22))
        #expect(o.banner(at: at(27.5)))
        #expect(!o.banner(at: at(28)))
    }

    @Test func aStallShowsTheBannerUntilItPlaysAgain() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(1))
        #expect(!o.banner(at: at(10)))
        o.playback(playing: false, at: at(10))
        #expect(o.banner(at: at(60)), "stalled or failed: it stays")
        o.playback(playing: true, at: at(61))
        #expect(o.banner(at: at(66.5)))
        #expect(!o.banner(at: at(67)))
    }

    @Test func playingTwiceDoesNotRestartTheTime() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        o.playback(playing: true, at: at(5))
        #expect(!o.banner(at: at(6)))
    }

    @Test func infoShowsTheBannerItsFullTime() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        o.showBanner(at: at(30))
        #expect(o.banner(at: at(35.9)))
        #expect(!o.banner(at: at(36)))
    }

    @Test func backPutsTheBannerAwayThenLeaves() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        var back = o.back(at: at(1))
        #expect(back, "the banner goes first")
        #expect(!o.banner(at: at(1)))
        #expect(o.nextChange(after: at(1)) == nil)
        back = o.back(at: at(2))
        #expect(!back, "nothing left: the player closes")
        o.showBanner(at: at(3))
        #expect(o.banner(at: at(3)), "info brings it back")
    }

    @Test func backLeavesAChannelThatDoesNotPlay() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        let back = o.back(at: at(1))
        #expect(!back, "the banner is the tune's state, not something to put away")
        #expect(o.banner(at: at(1)))
    }

    @Test func theMiniGuideGoesAfterItsTime() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        o.toggleMiniGuide(at: at(2))
        #expect(o.miniGuide(at: at(11.9)))
        #expect(!o.banner(at: at(3)), "never under the mini-guide")
        #expect(!o.miniGuide(at: at(12)), "it does not stay up for good")
        #expect(o.nextChange(after: at(7)) == at(12))
    }

    @Test func surfingInTheMiniGuideKeepsIt() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        o.toggleMiniGuide(at: at(1))
        o.tuned(at: at(9))
        #expect(o.miniGuide(at: at(15)), "it stays while the next channel starts")
        o.playback(playing: true, at: at(10))
        #expect(o.miniGuide(at: at(19.9)))
        #expect(!o.miniGuide(at: at(20)))
        #expect(!o.banner(at: at(20)), "the banner's time went by under it")
    }

    /// The bug: a buffering stall did not change the playback phase, so the banner
    /// did not come back. A stall that lasts shows the banner and the buffering state; when
    /// the picture moves again buffering goes at once and the banner its full time later.
    @Test func aBufferingStallShowsTheBannerUntilThePictureMoves() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(1))
        #expect(!o.banner(at: at(10)))
        o.stalled(at: at(10))
        #expect(o.playing, "buffering is not a stop")
        #expect(!o.buffering(at: at(10.9)) && !o.banner(at: at(10.9)), "nothing for the grace")
        #expect(o.nextChange(after: at(10)) == at(11), "the player wakes when it shows")
        #expect(o.buffering(at: at(11)) && o.banner(at: at(11)))
        #expect(o.banner(at: at(60)) && o.buffering(at: at(60)), "it stays while the picture waits")
        #expect(o.nextChange(after: at(60)) == nil, "nothing goes on its own while it buffers")
        let back = o.back(at: at(60))
        #expect(!back, "Menu leaves a channel that does not play")
        o.resumed(at: at(61))
        #expect(!o.buffering(at: at(61)), "buffering goes as soon as the picture moves")
        #expect(o.banner(at: at(66.9)))
        #expect(!o.banner(at: at(67)), "the banner its full time after the resume")
        #expect(o.nextChange(after: at(61)) == at(67))
    }

    @Test func aHiccupShowsNothing() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        o.stalled(at: at(20))
        o.resumed(at: at(20.5))
        #expect(!o.buffering(at: at(21)) && !o.banner(at: at(21)))
        #expect(o.nextChange(after: at(20.5)) == nil)
    }

    @Test func aStallBeforePlayingIsStillStarting() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.stalled(at: at(1))
        #expect(!o.buffering(at: at(5)), "a channel that does not play yet is starting, not buffering")
        #expect(o.banner(at: at(5)))
    }

    @Test func aStallAfterDismissingTheBannerShowsItAgain() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        _ = o.back(at: at(1))
        #expect(!o.banner(at: at(2)))
        o.stalled(at: at(3))
        #expect(o.banner(at: at(4)), "a stall brings it back")
        o.resumed(at: at(5))
        #expect(o.banner(at: at(10.9)) && !o.banner(at: at(11)))
    }

    @Test func theMiniGuidesTimeStandsStillWhileBuffering() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        o.toggleMiniGuide(at: at(1))
        o.stalled(at: at(5))
        #expect(o.miniGuide(at: at(30)), "it stays while the channel buffers")
        #expect(!o.banner(at: at(30)), "the mini-guide names the channel")
        o.resumed(at: at(31))
        #expect(o.miniGuide(at: at(40.9)))
        #expect(!o.miniGuide(at: at(41)), "its full time after the resume")
    }

    @Test func aMiniGuideThatWentStaysGone() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        o.toggleMiniGuide(at: at(1))
        #expect(!o.miniGuide(at: at(12)))
        o.tuned(at: at(20))
        #expect(!o.miniGuide(at: at(20)), "a tune does not bring it back")
        o.stalled(at: at(21))
        o.playback(playing: true, at: at(21))
        o.toggleMiniGuide(at: at(30))
        #expect(o.miniGuide(at: at(30)), "Select opens it at once")
    }

    @Test func theOverlaysKeepABottomCornerLogoClear() {
        #expect(LiveOverlays.edge(clearing: nil) == .bottom)
        #expect(LiveOverlays.edge(clearing: .topRight) == .bottom)
        #expect(LiveOverlays.edge(clearing: .topLeft) == .bottom)
        #expect(LiveOverlays.edge(clearing: .bottomRight) == .top)
        #expect(LiveOverlays.edge(clearing: .bottomLeft) == .top)
    }

    @Test func backAndSelectCloseTheMiniGuide() {
        var o = LiveOverlays()
        o.tuned(at: at(0))
        o.playback(playing: true, at: at(0))
        o.toggleMiniGuide(at: at(1))
        let back = o.back(at: at(2))
        #expect(back)
        #expect(!o.miniGuide(at: at(2)))
        #expect(o.banner(at: at(2)), "the banner it covered is still in its time")
        o.toggleMiniGuide(at: at(3))
        o.toggleMiniGuide(at: at(4))
        #expect(!o.miniGuide(at: at(4)))
    }
}
