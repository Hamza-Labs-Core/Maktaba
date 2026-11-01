import MaktabaKit
import SwiftUI

/// What the Live TV section knows (FR-21, FR-22): what is on now on every channel the person
/// may read (`GET /api/channels/now`) and the guide's window (`GET /api/channels/guide`).
/// Asked again once a minute, only while a Live TV screen shows (NFR-16). A channel the person
/// may not read is never here: the home leaves it out (FR-24).
@MainActor @Observable
final class LiveModel {
    private(set) var now: [ChannelNow] = []
    private(set) var guide: GuideWindow?
    private(set) var problem: HomeFailure?
    private(set) var loading = true

    /// The guide's day: 0 is today (from the half hour before now, `hours` long), 1 tomorrow
    /// and so on (that whole day).
    var day = 0
    /// How many hours the guide shows today: three on a phone, six on a tablet (FR-22).
    var hours: Double = 3
    /// The programme whose sheet is open (the guide sets it; the Live TV screen shows it).
    var programme: GuideBlock?

    /// How often a showing Live TV screen asks again.
    static let refresh: Duration = .seconds(60)
    /// The days the day picker offers, today first.
    static let days = 7

    /// The channels in number order: the order the player's next and previous go in. A
    /// channel only the guide names is added (both are the home's lineup).
    var channels: [LiveChannel] {
        var seen = Set<String>()
        var all: [LiveChannel] = []
        for c in now.map(\.channel) + (guide?.channels.map(\.channel) ?? []) where seen.insert(c.id).inserted {
            all.append(c)
        }
        return all.sorted { ($0.number, $0.name) < ($1.number, $1.name) }
    }

    /// What is on now and next on a channel.
    func onNow(_ channel: String) -> ChannelNow? { now.first { $0.channel.id == channel } }

    /// One channel's row in the guide.
    func row(_ channel: String) -> ChannelGuide? { guide?.channels.first { $0.channel.id == channel } }

    /// Asks again every minute while the calling task lives (a screen's `.task`). `guide`:
    /// the guide's window too (the player needs only what is on now).
    func keepFresh(_ client: APIClient, guide withGuide: Bool = true) async {
        while !Task.isCancelled {
            await load(client, guide: withGuide)
            try? await Task.sleep(for: Self.refresh)
        }
    }

    func load(_ client: APIClient, guide withGuide: Bool = true) async {
        do {
            async let n = client.channelsNow()
            if withGuide {
                let (from, to) = window(at: .now)
                async let g = client.guide(from: from, to: to)
                let (nowAnswer, guideAnswer) = try await (n, g)
                now = nowAnswer.channels
                guide = guideAnswer
            } else {
                now = try await n.channels
            }
            problem = nil
        } catch {
            if !Log.isCancellation(error) {
                Log.failure("load live tv", error)
                problem = HomeFailure(error)
            }
        }
        loading = false
    }

    /// The guide again (another day, another span), what is on now kept.
    func loadGuide(_ client: APIClient) async {
        do {
            let (from, to) = window(at: .now)
            guide = try await client.guide(from: from, to: to)
            problem = nil
        } catch {
            if !Log.isCancellation(error) {
                Log.failure("load the guide", error)
                problem = HomeFailure(error)
            }
        }
    }

    /// The guide's window at `date` for the chosen day.
    func window(at date: Date, calendar: Calendar = .current) -> (Date, Date) {
        Self.window(day: day, hours: hours, at: date, calendar: calendar)
    }

    static func window(day: Int, hours: Double, at date: Date, calendar: Calendar = .current) -> (Date, Date) {
        guard day > 0, let start = calendar.date(byAdding: .day, value: day, to: calendar.startOfDay(for: date)),
              let end = calendar.date(byAdding: .day, value: 1, to: start) else {
            let start = halfHour(date)
            return (start, start.addingTimeInterval(hours * 3600))
        }
        return (start, end)
    }

    /// The half hour at or before `date`: the guide's first column today.
    static func halfHour(_ date: Date) -> Date {
        Date(timeIntervalSince1970: (date.timeIntervalSince1970 / 1800).rounded(.down) * 1800)
    }
}
