import AVKit
import Testing
@testable import MaktabaKit

/// A player that closes takes its Picture in Picture controller apart on the main thread and
/// keeps it a while. Left to go with its owner, the controller could be freed on AVKit's own
/// queue while the player changed, and its `dealloc` then took a view out of the window off
/// the main thread: an Auto Layout exception that ended the app (an iPad leaving a live
/// channel after Live TV went off, CI 2026-11-12).
@MainActor @Suite(.serialized) struct PictureInPictureTests {
    private func kept(_ c: AVPictureInPictureController?) -> Bool {
        PictureInPicture.retired.contains { $0 === c }
    }

    private func wait(_ done: () -> Bool) async {
        for _ in 0..<200 where !done() { try? await Task.sleep(for: .milliseconds(25)) }
    }

    @Test func closingTakesTheControllerOffItsLayerAndKeepsItAWhile() async throws {
        let player = AVPlayer(), layer = AVPlayerLayer(player: player)
        let pip = PictureInPicture(linger: .milliseconds(200))
        pip.attach(layer)
        // a host without Picture in Picture makes no controller: nothing to take apart
        guard let made = pip.controllerForTests else { return }
        #expect(pip.isAttached)
        #expect(made.contentSource != nil)

        pip.close()
        #expect(!pip.isAttached)
        #expect(!pip.isActive && !pip.isPossible)
        #expect(made.contentSource == nil, "off its layer before anything lets it go")
        #expect(made.delegate == nil)
        #expect(kept(made), "kept past the close")
        pip.toggle() // nothing to start: no controller
        pip.stop()

        await wait { !kept(made) }
        #expect(!kept(made), "let go, on the main thread, once the wait is over")
    }

    @Test func closingTwiceAndWithNothingAttachedDoesNothing() {
        let pip = PictureInPicture(linger: .milliseconds(50))
        let before = PictureInPicture.retired.count
        pip.close()
        pip.close()
        #expect(PictureInPicture.retired.count == before)
    }

    @Test func theSameLayerAttachesAgainAfterAClose() {
        let layer = AVPlayerLayer(player: AVPlayer())
        let pip = PictureInPicture(linger: .milliseconds(50))
        pip.attach(layer)
        guard let first = pip.controllerForTests else { return }
        pip.attach(layer)
        #expect(pip.controllerForTests === first, "the same layer: the same controller")
        pip.close()
        pip.attach(layer)
        #expect(pip.isAttached)
        #expect(pip.controllerForTests !== first)
        pip.close()
    }

    @Test func aNewLayerRetiresTheControllerOfTheOneBefore() {
        let pip = PictureInPicture(linger: .milliseconds(50))
        pip.attach(AVPlayerLayer(player: AVPlayer()))
        guard let first = pip.controllerForTests else { return }
        let second = AVPlayerLayer(player: AVPlayer())
        pip.attach(second)
        #expect(first.contentSource == nil)
        #expect(kept(first))
        pip.close()
    }

    @Test func anObjectThatGoesUnclosedIsTakenApartOnTheMainThread() async {
        var pip: PictureInPicture? = PictureInPicture(linger: .milliseconds(200))
        let layer = AVPlayerLayer(player: AVPlayer())
        pip?.attach(layer)
        guard let made = pip?.controllerForTests else { return }
        pip = nil
        await wait { made.contentSource == nil }
        #expect(made.contentSource == nil)
        #expect(made.delegate == nil)
    }
}
