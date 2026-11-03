import Foundation

/// What a live player draws over the picture (the channel banner, the mini-guide, the
/// buffering state) and when each goes away. A value: the player feeds it what happened and
/// when, and asks what shows at a moment (the clock is the caller's, a fake one in tests).
///
/// - The banner comes on each tune, on a stall (the channel stopped playing) and on the
///   remote's info press. While nothing plays it stays (the tune's state is what the screen
///   says); once the channel plays it goes `bannerTime` later, counted from whichever is
///   later: the tune (or the press), or the moment playback started. A slow start (an
///   encoder warming up for longer than `bannerTime`) therefore still hides it.
/// - A buffering stall (the picture waits for data while the channel still plays: AVPlayer's
///   `timeControlStatus` waiting, or its stall notice) that lasts `stallGrace` is buffering:
///   the banner comes back with the buffering state, and both stay while it lasts. When the
///   picture moves again the buffering state goes at once and the banner `bannerTime` later,
///   as after a slow start. A shorter wait shows nothing (no flicker on a hiccup).
/// - The mini-guide opens on Select and goes the same way, `miniGuideTime` after it opened,
///   the last channel change in it, or playback starting, whichever is latest. Its time
///   stands still while the channel buffers.
/// - Back (Menu) closes the mini-guide first, then a banner over a playing channel; only then
///   does it leave the player.
/// - The overlays sit at the bottom of the picture, unless the home burns the channel's logo
///   into a bottom corner: then at the top (`edge(clearing:)`), so they never cover it.
public struct LiveOverlays: Equatable, Sendable {
    public typealias Instant = ContinuousClock.Instant

    /// How long the banner stays once the channel plays.
    public static let bannerTime: Duration = .seconds(6)
    /// How long the mini-guide stays once the channel plays, with no channel change in it.
    public static let miniGuideTime: Duration = .seconds(10)
    /// How long the picture may wait before the wait shows as buffering.
    public static let stallGrace: Duration = .seconds(1)

    /// Whether the channel is playing now (a buffering stall does not change it).
    public private(set) var playing = false
    /// When the channel started playing, or last moved again after buffering (nil while it
    /// does not play).
    private var playingSince: Instant?
    /// The picture has waited since then (nil: it moves, or the channel does not play).
    private var stalledSince: Instant?
    /// The last tune, stall or info press: the banner shows from then (nil: never asked).
    private var bannerFrom: Instant?
    /// The viewer put the banner away (until the next tune, stall or press).
    private var bannerDismissed = false
    /// When the mini-guide opened or last changed channel (nil: closed).
    private var miniGuideFrom: Instant?

    public init() {}

    // MARK: what happened

    /// A channel was tuned (the first, or ▲ / ▼): the banner shows again, and an open
    /// mini-guide stays its full time from now. Playback starts over.
    public mutating func tuned(at t: Instant) {
        // one that went on its own stays gone
        let guide = miniGuide(at: t)
        playing = false
        playingSince = nil
        stalledSince = nil
        showBanner(at: t)
        miniGuideFrom = guide ? t : nil
    }

    /// The channel started playing, or stopped (a failure, the home ending it).
    public mutating func playback(playing now: Bool, at t: Instant) {
        guard now != playing else { return }
        playing = now
        stalledSince = nil
        if now {
            playingSince = t
        } else {
            playingSince = nil
            // stopped: the banner says which channel this is while it is not playing
            showBanner(at: t)
        }
    }

    /// The picture waits for data while the channel plays (a buffering stall). Nothing shows
    /// until it has waited `stallGrace`.
    public mutating func stalled(at t: Instant) {
        guard playing, stalledSince == nil else { return }
        stalledSince = t
    }

    /// The picture moves again. After a stall that showed, the banner stays `bannerTime` from
    /// now, and an open mini-guide its full time.
    public mutating func resumed(at t: Instant) {
        guard let since = stalledSince else { return }
        stalledSince = nil
        let shown = since + Self.stallGrace
        guard t >= shown else { return } // a hiccup: nothing showed, nothing changes
        if !guideOpen(at: shown) { miniGuideFrom = nil }
        playingSince = t
        bannerFrom = since
        bannerDismissed = false
    }

    /// The remote's info press: the banner again, its full time from now.
    public mutating func showBanner(at t: Instant) {
        bannerFrom = t
        bannerDismissed = false
    }

    /// Select (while the channel plays or starts): the mini-guide opens, or closes.
    public mutating func toggleMiniGuide(at t: Instant) {
        miniGuideFrom = miniGuide(at: t) ? nil : t
    }

    /// Back (Menu): closes the mini-guide, else puts away a banner over a playing channel.
    /// False when there was nothing to put away (the player then closes).
    public mutating func back(at t: Instant) -> Bool {
        if miniGuide(at: t) {
            miniGuideFrom = nil
            return true
        }
        if playing, !buffering(at: t), banner(at: t) {
            bannerDismissed = true
            return true
        }
        return false
    }

    // MARK: what shows

    /// Whether the picture is buffering at `t` (the player says so, over the picture).
    public func buffering(at t: Instant) -> Bool {
        guard playing, let since = stalledSince else { return false }
        return t >= since + Self.stallGrace
    }

    /// Whether the mini-guide shows at `t`.
    public func miniGuide(at t: Instant) -> Bool {
        // while buffering, its time stands at the moment the buffering showed
        if buffering(at: t), let since = stalledSince { return guideOpen(at: since + Self.stallGrace) }
        return guideOpen(at: t)
    }

    /// Whether the banner shows at `t` (never under the mini-guide, which names the channel).
    public func banner(at t: Instant) -> Bool {
        guard !miniGuide(at: t) else { return false }
        if buffering(at: t) { return true }
        guard !bannerDismissed, let from = bannerFrom else { return false }
        guard let until = deadline(from: from, lasting: Self.bannerTime) else { return true }
        return t < until
    }

    /// The next moment after `t` at which what shows changes on its own (nil: nothing will,
    /// e.g. while the channel is not playing, or while it buffers). The player wakes then to
    /// redraw.
    public func nextChange(after t: Instant) -> Instant? {
        if buffering(at: t) { return nil }
        var times: [Instant] = []
        if let since = stalledSince { times.append(since + Self.stallGrace) }
        if let from = miniGuideFrom, let until = deadline(from: from, lasting: Self.miniGuideTime) {
            times.append(until)
        }
        if !bannerDismissed, let from = bannerFrom, let until = deadline(from: from, lasting: Self.bannerTime) {
            times.append(until)
        }
        return times.filter { $0 > t }.min()
    }

    /// Where the banner and the mini-guide sit: the bottom of the picture, or the top when
    /// the channel's logo is burned into a bottom corner (nil: none, or an older home).
    public static func edge(clearing watermark: WatermarkCorner?) -> Edge {
        watermark?.isBottom == true ? .top : .bottom
    }

    public enum Edge: Sendable {
        case top, bottom
    }

    /// The mini-guide is open at `t`, buffering aside.
    private func guideOpen(at t: Instant) -> Bool {
        guard let from = miniGuideFrom else { return false }
        guard let until = deadline(from: from, lasting: Self.miniGuideTime) else { return true }
        return t < until
    }

    /// When something shown from `from` goes: `time` after the later of `from` and playback
    /// starting; nil while the channel is not playing (it stays).
    private func deadline(from: Instant, lasting time: Duration) -> Instant? {
        guard playing, let since = playingSince else { return nil }
        return max(from, since) + time
    }
}
