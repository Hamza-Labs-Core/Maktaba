import Foundation

/// What a live player draws over the picture (the channel banner, the mini-guide) and when
/// each goes away. A value: the player feeds it what happened and when, and asks what shows
/// at a moment (the clock is the caller's, a fake one in tests).
///
/// - The banner comes on each tune, on a stall (the channel stopped playing) and on the
///   remote's info press. While nothing plays it stays (the tune's state is what the screen
///   says); once the channel plays it goes `bannerTime` later, counted from whichever is
///   later: the tune (or the press), or the moment playback started. A slow start (an
///   encoder warming up for longer than `bannerTime`) therefore still hides it.
/// - The mini-guide opens on Select and goes the same way, `miniGuideTime` after it opened,
///   the last channel change in it, or playback starting, whichever is latest.
/// - Back (Menu) closes the mini-guide first, then a banner over a playing channel; only then
///   does it leave the player.
public struct LiveOverlays: Equatable, Sendable {
    public typealias Instant = ContinuousClock.Instant

    /// How long the banner stays once the channel plays.
    public static let bannerTime: Duration = .seconds(6)
    /// How long the mini-guide stays once the channel plays, with no channel change in it.
    public static let miniGuideTime: Duration = .seconds(10)

    /// Whether the channel is playing now.
    public private(set) var playing = false
    /// When the channel started playing (nil while it does not).
    private var playingSince: Instant?
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
        playing = false
        playingSince = nil
        showBanner(at: t)
        if miniGuideFrom != nil { miniGuideFrom = t }
    }

    /// The channel started playing, or stopped (a stall, a failure, the home ending it).
    public mutating func playback(playing now: Bool, at t: Instant) {
        guard now != playing else { return }
        playing = now
        if now {
            playingSince = t
        } else {
            playingSince = nil
            // a stall: the banner says which channel this is while it is not playing
            showBanner(at: t)
        }
    }

    /// The remote's info press: the banner again, its full time from now.
    public mutating func showBanner(at t: Instant) {
        bannerFrom = t
        bannerDismissed = false
    }

    /// Select (while the channel plays or starts): the mini-guide opens, or closes.
    public mutating func toggleMiniGuide(at t: Instant) {
        miniGuideFrom = miniGuideFrom == nil ? t : nil
    }

    /// Back (Menu): closes the mini-guide, else puts away a banner over a playing channel.
    /// False when there was nothing to put away (the player then closes).
    public mutating func back(at t: Instant) -> Bool {
        if miniGuide(at: t) {
            miniGuideFrom = nil
            return true
        }
        if playing, banner(at: t) {
            bannerDismissed = true
            return true
        }
        return false
    }

    // MARK: what shows

    /// Whether the mini-guide shows at `t`.
    public func miniGuide(at t: Instant) -> Bool {
        guard let from = miniGuideFrom else { return false }
        guard let until = deadline(from: from, lasting: Self.miniGuideTime) else { return true }
        return t < until
    }

    /// Whether the banner shows at `t` (never under the mini-guide, which names the channel).
    public func banner(at t: Instant) -> Bool {
        guard !miniGuide(at: t), !bannerDismissed, let from = bannerFrom else { return false }
        guard let until = deadline(from: from, lasting: Self.bannerTime) else { return true }
        return t < until
    }

    /// The next moment after `t` at which something that shows goes away (nil: nothing will
    /// on its own, e.g. while the channel is not playing). The player wakes then to redraw.
    public func nextChange(after t: Instant) -> Instant? {
        var times: [Instant] = []
        if let from = miniGuideFrom, let until = deadline(from: from, lasting: Self.miniGuideTime) {
            times.append(until)
        }
        if !bannerDismissed, let from = bannerFrom, let until = deadline(from: from, lasting: Self.bannerTime) {
            times.append(until)
        }
        return times.filter { $0 > t }.min()
    }

    /// When something shown from `from` goes: `time` after the later of `from` and playback
    /// starting; nil while the channel is not playing (it stays).
    private func deadline(from: Instant, lasting time: Duration) -> Instant? {
        guard playing, let since = playingSince else { return nil }
        return max(from, since) + time
    }
}
