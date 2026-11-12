import MaktabaKit
import SwiftUI
#if canImport(UIKit)
import UIKit
#endif

// Epic 57 on iPhone and iPad: the blocked page (57.05), Ask a parent and the parent PIN
// (57.06), the time's-up screen and the time left (57.15), the live slate (57.10). Every
// word is MaktabaKit's (`ParentalWords`, its catalog: English and Arabic under the epic's
// keys), so these views draw text verbatim.

/// What the Player drops when the account's limit changes: the shelves kept on the device,
/// the pictures in memory and the play queue it holds, so nothing hidden lingers.
@MainActor
enum ParentalCaches {
    static func drop(home: URL, user: String) {
        PlayerQueues.forget(home)
        let kept = HomeCache.of(home: home, user: user)
        Task {
            await ImageCache.shared.clearMemory()
            await kept?.clear()
        }
    }
}

/// "This isn't available": a lock, the reason when the home says the account or the device
/// is limited, Ask a parent, Enter parent PIN, Back. Never a title, a poster or a rating:
/// the home sends none.
struct BlockedPageView: View {
    let context: PlayerContext
    /// The line under the title instead of the limit's reason (the player, mid-video).
    var stoppedLine: String?
    let back: () -> Void
    /// A parent opened it (a right PIN): the screen reads the home again.
    var opened: () -> Void = {}

    @State private var page: BlockedPage
    @State private var enteringPIN = false

    init(context: PlayerContext, subject: ParentalSubject, stoppedLine: String? = nil, back: @escaping () -> Void,
         opened: @escaping () -> Void = {}) {
        self.context = context
        self.stoppedLine = stoppedLine
        self.back = back
        self.opened = opened
        _page = State(initialValue: BlockedPage(client: context.client, subject: subject, limit: context.session.parental.current))
    }

    var body: some View {
        ScrollView {
            VStack(spacing: MaktabaTokens.Spacing.s4) {
                Image(systemName: "lock.fill")
                    .font(.system(size: 44))
                    .foregroundStyle(Color.brass)
                    .accessibilityLabel(Text(verbatim: ParentalWords.text("parental.blocked.a11y")))
                    .accessibilityIdentifier("blocked.lock")
                Text(verbatim: ParentalWords.text("parental.blocked.title"))
                    .font(.maktabaHeading(26, relativeTo: .title))
                    .foregroundStyle(Color.mktText)
                    .multilineTextAlignment(.center)
                    .accessibilityAddTraits(.isHeader)
                    .accessibilityIdentifier("blocked.title")
                if let line = stoppedLine ?? page.reasonLine {
                    Text(verbatim: line)
                        .font(.maktabaBody(16, relativeTo: .body))
                        .foregroundStyle(Color.mktMuted)
                        .multilineTextAlignment(.center)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("blocked.reason")
                }
                VStack(spacing: MaktabaTokens.Spacing.s3) {
                    if page.canAsk {
                        Button {
                            Task { await page.askParent() }
                        } label: {
                            Text(verbatim: ParentalWords.text(page.ask == .pending ? "parental.ask.pending" : "parental.blocked.ask"))
                                .frame(maxWidth: .infinity, minHeight: 44)
                        }
                        .buttonStyle(.borderedProminent)
                        .tint(Color.brass)
                        .foregroundStyle(MaktabaTokens.Colors.accentFg.color)
                        .disabled(page.ask == .sending || page.ask == .pending || page.ask == .tooMany)
                        .accessibilityIdentifier("blocked.ask")
                    }
                    if page.canEnterPIN {
                        Button {
                            enteringPIN = true
                        } label: {
                            Text(verbatim: ParentalWords.text("parental.blocked.pin"))
                                .frame(maxWidth: .infinity, minHeight: 44)
                        }
                        .buttonStyle(.bordered)
                        .tint(Color.brass)
                        .accessibilityIdentifier("blocked.pin")
                    }
                    Button(action: back) {
                        Text(verbatim: ParentalWords.text("parental.blocked.back"))
                            .frame(maxWidth: .infinity, minHeight: 44)
                            .contentShape(Rectangle())
                    }
                    .buttonStyle(.borderless)
                    .frame(minHeight: 44)
                    .foregroundStyle(Color.mktBrassText)
                    .accessibilityIdentifier("blocked.back")
                }
                .frame(maxWidth: 360)
                if let line = page.askLine() {
                    Text(verbatim: line)
                        .font(.maktabaBody(15, relativeTo: .subheadline))
                        .foregroundStyle(Color.mktMuted)
                        .multilineTextAlignment(.center)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("blocked.askLine")
                }
            }
            .padding(MaktabaTokens.Spacing.s6)
            .frame(maxWidth: .infinity)
        }
        .scrollBounceBehavior(.basedOnSize)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen.blocked")
        .task { await page.load() }
        // a request waits: the home is asked every 30 s whether the limit changed (a parent
        // answered); the screen behind reads again when it did
        .task(id: page.ask == .pending) {
            guard page.ask == .pending else { return }
            while !Task.isCancelled {
                do { try await Task.sleep(for: .seconds(30)) } catch { return }
                await context.session.parental.refresh()
            }
        }
        .sheet(isPresented: $enteringPIN) {
            PinSheet(client: context.client, subject: page.subject, people: page.unlockers) {
                enteringPIN = false
                Task {
                    await context.session.parental.refresh()
                    opened()
                }
            }
        }
    }
}

/// Enter parent PIN: who is typing it, then six digits on a pad whose keys keep their phone
/// order in Arabic too (the pad and the dots are left to right whatever the layout).
struct PinSheet: View {
    let client: APIClient
    let subject: ParentalSubject
    let people: [ParentalPerson]
    let unlocked: () -> Void

    @State private var entry: PinEntry?
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            Group {
                if let entry {
                    pad(entry)
                } else {
                    chooser
                }
            }
            .maktabaSurface()
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { dismiss() } label: { Text(verbatim: ParentalWords.text("parental.pin.cancel")) }
                        .accessibilityIdentifier("pin.cancel")
                }
            }
        }
        .onAppear {
            // one person with a PIN: no question to ask
            if entry == nil, people.count == 1, let only = people.first { choose(only) }
        }
    }

    private func choose(_ person: ParentalPerson) {
        entry = PinEntry(client: client, subject: subject, guardian: person)
    }

    private var chooser: some View {
        List {
            Section {
                ForEach(people) { person in
                    Button { choose(person) } label: {
                        Label { Text(verbatim: person.username) } icon: { Image(systemName: "person.crop.circle") }
                            .frame(minHeight: 44)
                    }
                    .foregroundStyle(Color.mktText)
                    .accessibilityIdentifier("pin.person.\(person.id)")
                }
            } header: {
                Text(verbatim: ParentalWords.text("parental.pin.choose")).accessibilityAddTraits(.isHeader)
            }
        }
        .accessibilityIdentifier("pin.chooser")
    }

    private func pad(_ entry: PinEntry) -> some View {
        ScrollView {
            VStack(spacing: MaktabaTokens.Spacing.s5) {
                Text(verbatim: entry.guardian.username)
                    .font(.maktabaBody(15, relativeTo: .subheadline))
                    .foregroundStyle(Color.mktMuted)
                Text(verbatim: ParentalWords.text("parental.pin.enter"))
                    .font(.maktabaBody(18, weight: .semibold, relativeTo: .title3))
                    .foregroundStyle(Color.mktText)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
                HStack(spacing: MaktabaTokens.Spacing.s3) {
                    ForEach(0..<PinEntry.length, id: \.self) { i in
                        Circle()
                            .fill(i < entry.digits.count ? Color.brass : Color.clear)
                            .overlay(Circle().stroke(Color.mktMuted, lineWidth: 1.5))
                            .frame(width: 16, height: 16)
                    }
                }
                .environment(\.layoutDirection, .leftToRight)
                .accessibilityElement()
                .accessibilityLabel(Text(verbatim: ParentalWords.text("parental.pin.digits", entry.digits.count)))
                .accessibilityIdentifier("pin.dots")
                Group {
                    if entry.state == .checking {
                        ProgressView()
                    } else if let line = entry.line() {
                        Text(verbatim: line)
                            .foregroundStyle(entry.state == .unlocked ? Color.mktText : MaktabaTokens.Colors.error.color)
                            .multilineTextAlignment(.center)
                            .fixedSize(horizontal: false, vertical: true)
                            .accessibilityIdentifier("pin.line")
                    } else {
                        Text(verbatim: " ")
                    }
                }
                .font(.maktabaBody(15, relativeTo: .subheadline))
                .frame(minHeight: 24)
                keys(entry)
            }
            .padding(MaktabaTokens.Spacing.s6)
            .frame(maxWidth: .infinity)
        }
        .scrollBounceBehavior(.basedOnSize)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("pin.pad")
        .onChange(of: entry.complete) { _, complete in
            if complete { Task { await entry.submit() } }
        }
        .onChange(of: entry.state) { _, state in
            guard state == .unlocked else { return }
            Task {
                try? await Task.sleep(for: .milliseconds(700))
                unlocked()
            }
        }
    }

    /// 1 2 3 / 4 5 6 / 7 8 9 / 0 ⌫, left to right in every language (as the phone's own pad).
    private func keys(_ entry: PinEntry) -> some View {
        VStack(spacing: MaktabaTokens.Spacing.s3) {
            ForEach(PinPad.rows, id: \.self) { row in
                HStack(spacing: MaktabaTokens.Spacing.s3) {
                    ForEach(row, id: \.self) { d in key(d, entry) }
                }
            }
            HStack(spacing: MaktabaTokens.Spacing.s3) {
                Color.clear.frame(width: 72, height: 56)
                key(0, entry)
                Button { entry.delete() } label: {
                    Image(systemName: "delete.backward")
                        .font(.title2)
                        .frame(width: 72, height: 56)
                }
                .buttonStyle(.plain)
                .foregroundStyle(Color.mktText)
                .disabled(!entry.canType || entry.digits.isEmpty)
                .accessibilityLabel(Text(verbatim: ParentalWords.text("parental.pin.delete")))
                .accessibilityIdentifier("pin.delete")
            }
        }
        .environment(\.layoutDirection, .leftToRight)
    }

    private func key(_ d: Int, _ entry: PinEntry) -> some View {
        Button { entry.press(d) } label: {
            // Western digits on the keys, as the phone's pad shows them in Arabic too
            Text(verbatim: String(d))
                .font(.system(.title, design: .rounded).weight(.medium))
                .frame(width: 72, height: 56)
                .background(Color.mktRaised, in: .rect(cornerRadius: MaktabaTokens.Radius.lg))
        }
        .buttonStyle(.plain)
        .foregroundStyle(Color.mktText)
        .disabled(!entry.canType)
        .accessibilityIdentifier("pin.key.\(d)")
    }
}

// MARK: time (57.15)

/// "Time's up for today" (or "Not watching time yet"): when watching opens again, where the
/// video stopped, and who to ask. Playback stopped and is not tried again from here; the
/// home is asked every 30 s (and when the app comes back) whether time was given, and the
/// video goes on by itself when it was.
struct TimeUpView: View {
    let context: PlayerContext
    let up: TimeUp
    /// Where the video stopped, in seconds (nil: nothing was playing).
    var position: Double?
    let back: () -> Void
    /// The home allows watching again (a parent gave more time).
    var allowed: () -> Void = {}

    @Environment(\.scenePhase) private var scenePhase
    /// Ask for more time (57.06): the minutes chosen, the request's state, who has a PIN.
    @State private var minutes = 30
    @State private var page: BlockedPage?
    @State private var asked: BlockedPage?
    @State private var enteringPIN = false

    var body: some View {
        ScrollView {
            VStack(spacing: MaktabaTokens.Spacing.s4) {
                Image(systemName: "hourglass.bottomhalf.filled")
                    .font(.system(size: 44))
                    .foregroundStyle(Color.brass)
                    .accessibilityHidden(true)
                Text(verbatim: up.title())
                    .font(.maktabaHeading(26, relativeTo: .title))
                    .foregroundStyle(Color.mktText)
                    .multilineTextAlignment(.center)
                    .accessibilityAddTraits(.isHeader)
                    .accessibilityIdentifier("timeUp.title")
                if let next = up.nextLine() {
                    Text(verbatim: next)
                        .foregroundStyle(Color.mktText)
                        .multilineTextAlignment(.center)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("timeUp.next")
                }
                if let position, position >= 1 {
                    Text(verbatim: ParentalWords.text("parental.time.saved", Self.clock(position)))
                        .foregroundStyle(Color.mktMuted)
                        .multilineTextAlignment(.center)
                        .accessibilityIdentifier("timeUp.saved")
                }
                Text(verbatim: ParentalWords.text("parental.time.askParent"))
                    .foregroundStyle(Color.mktMuted)
                    .multilineTextAlignment(.center)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("timeUp.ask")
                if let page, page.canAsk {
                    Picker(selection: $minutes) {
                        ForEach(ParentalSubject.timeChoices, id: \.self) { m in
                            Text(verbatim: ParentalWords.length(seconds: m * 60)).tag(m)
                        }
                    } label: {
                        Text(verbatim: ParentalWords.text("parental.time.askMore"))
                    }
                    .pickerStyle(.segmented)
                    .frame(maxWidth: 320)
                    .accessibilityIdentifier("timeUp.minutes")
                    Button {
                        let ask = page.asking(.time(minutes: minutes))
                        asked = ask
                        Task { await ask.askParent() }
                    } label: {
                        Text(verbatim: ParentalWords.text(asked?.ask == .pending ? "parental.ask.pending" : "parental.time.askMore"))
                            .frame(maxWidth: 320, minHeight: 44)
                    }
                    .buttonStyle(.borderedProminent)
                    .tint(Color.brass)
                    .foregroundStyle(MaktabaTokens.Colors.accentFg.color)
                    .disabled(asked?.ask == .sending || asked?.ask == .pending || asked?.ask == .tooMany)
                    .accessibilityIdentifier("timeUp.askMore")
                    if page.canEnterPIN {
                        Button { enteringPIN = true } label: {
                            Text(verbatim: ParentalWords.text("parental.blocked.pin")).frame(maxWidth: 320, minHeight: 44)
                        }
                        .buttonStyle(.bordered)
                        .tint(Color.brass)
                        .accessibilityIdentifier("timeUp.pin")
                    }
                    if let line = asked?.askLine() {
                        Text(verbatim: line)
                            .font(.maktabaBody(15, relativeTo: .subheadline))
                            .foregroundStyle(Color.mktMuted)
                            .multilineTextAlignment(.center)
                            .fixedSize(horizontal: false, vertical: true)
                            .accessibilityIdentifier("timeUp.askLine")
                    }
                }
                Button(action: back) {
                    Text(verbatim: ParentalWords.text("parental.blocked.back"))
                        .frame(maxWidth: 320, minHeight: 44)
                }
                .buttonStyle(.bordered)
                .tint(Color.brass)
                .accessibilityIdentifier("timeUp.back")
            }
            .font(.maktabaBody(16, relativeTo: .body))
            .padding(MaktabaTokens.Spacing.s6)
            .frame(maxWidth: .infinity)
        }
        .scrollBounceBehavior(.basedOnSize)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen.timeUp")
        .task {
            let p = BlockedPage(client: context.client, subject: .time(minutes: minutes), limit: context.session.parental.current)
            await p.load()
            page = p
        }
        .sheet(isPresented: $enteringPIN) {
            PinSheet(client: context.client, subject: .time(minutes: minutes), people: page?.unlockers ?? []) {
                enteringPIN = false
                Task {
                    await context.session.parental.refreshTime()
                    allowed()
                }
            }
        }
        .task(id: scenePhase == .active) {
            guard scenePhase == .active else { return }
            let store = context.session.parental
            while !Task.isCancelled {
                await store.refreshTime()
                if let t = store.time?.state, t.allowed {
                    allowed()
                    return
                }
                do { try await Task.sleep(for: .seconds(30)) } catch { return }
            }
        }
    }

    /// A position as a clock ("12:05", "1:02:03"), digits left to right.
    static func clock(_ seconds: Double) -> String {
        "\u{200E}" + Duration.seconds(seconds.rounded(.down)).formatted(.time(pattern: seconds >= 3600 ? .hourMinuteSecond : .minuteSecond))
    }
}

/// The time left today, on Home and over the player, in the last 15 minutes; and the
/// warnings at 5 minutes and at 1 minute, shown for a moment. All from the home's answer.
struct TimeLeftBar: View {
    let store: ParentalStore
    /// Over the video: light text on a dark capsule.
    var overVideo = false
    /// Ask the home again every 30 s while this shows (the player; Home asks as the store does).
    var polls = false

    @State private var warning: Int?
    @State private var last: Int?

    var body: some View {
        TimelineView(.periodic(from: .now, by: 5)) { timeline in
            let left = TimeLeft.seconds(store.time, at: timeline.date)
            Group {
                if let warning {
                    label(TimeLeft.warningLine(warning), strong: true)
                        .accessibilityIdentifier("time.warning")
                } else if let line = TimeLeft.line(store.time, at: timeline.date) {
                    label(line, strong: false)
                        .accessibilityIdentifier("time.left")
                }
            }
            .onChange(of: left, initial: true) { before, now in
                if let due = TimeLeft.warning(from: last ?? before, to: now), due != warning { warn(due) }
                last = now
            }
        }
        .task(id: polls) {
            guard polls else { return }
            while !Task.isCancelled {
                await store.refreshTime()
                do { try await Task.sleep(for: .seconds(30)) } catch { return }
            }
        }
    }

    private func warn(_ threshold: Int) {
        warning = threshold
        #if os(iOS)
        UIAccessibility.post(notification: .announcement, argument: TimeLeft.warningLine(threshold))
        #endif
        Task {
            try? await Task.sleep(for: .seconds(8))
            if warning == threshold { warning = nil }
        }
    }

    private func label(_ text: String, strong: Bool) -> some View {
        Label { Text(verbatim: text) } icon: { Image(systemName: "hourglass") }
            .font(.maktabaBody(15, weight: strong ? .semibold : .medium, relativeTo: .subheadline))
            .foregroundStyle(overVideo ? Color.white : Color.mktText)
            .padding(.horizontal, MaktabaTokens.Spacing.s4)
            .padding(.vertical, MaktabaTokens.Spacing.s2)
            .background(overVideo ? AnyShapeStyle(.black.opacity(0.6)) : AnyShapeStyle(Color.mktSurface), in: .capsule)
            .accessibilityElement(children: .combine)
    }
}

/// Home's line when the home says the time is over: the same words as the time's-up screen.
struct TimeUpBanner: View {
    let up: TimeUp

    var body: some View {
        VStack(spacing: 2) {
            Text(verbatim: up.title()).font(.maktabaBody(15, weight: .semibold, relativeTo: .subheadline))
            if let next = up.nextLine() {
                Text(verbatim: next).font(.maktabaBody(13, relativeTo: .footnote)).foregroundStyle(Color.mktMuted)
            }
        }
        .foregroundStyle(Color.mktText)
        .multilineTextAlignment(.center)
        .padding(.horizontal, MaktabaTokens.Spacing.s4)
        .padding(.vertical, MaktabaTokens.Spacing.s2)
        .background(Color.mktSurface, in: .rect(cornerRadius: MaktabaTokens.Radius.lg))
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("time.up.banner")
    }
}

extension View {
    /// The time left today (or that it is over) at the top of a section's first screen, for
    /// an account the home keeps time for; nothing for anyone else.
    func parentalTime(_ store: ParentalStore) -> some View {
        safeAreaInset(edge: .top, spacing: 0) {
            if store.current.restricted, let reading = store.time {
                Group {
                    if let up = TimeUp(reading.state) {
                        TimeUpBanner(up: up)
                    } else {
                        TimeLeftBar(store: store)
                    }
                }
                .frame(maxWidth: .infinity)
                .padding(.vertical, MaktabaTokens.Spacing.s1)
            }
        }
    }
}

// MARK: live TV (57.10)

/// The slate over a channel whose programme now is above the limit: no title, only that it
/// is not available and that the channel comes back with the next programme.
struct LiveSlate: View {
    let back: () -> Void

    var body: some View {
        VStack(spacing: MaktabaTokens.Spacing.s4) {
            Image(systemName: "lock.fill")
                .font(.system(size: 44))
                .foregroundStyle(Color.brass)
                .accessibilityLabel(Text(verbatim: ParentalWords.text("parental.blocked.a11y")))
            Text(verbatim: ParentalWords.text("live.parental.slate"))
                .font(.maktabaBody(18, weight: .medium, relativeTo: .title3))
                .foregroundStyle(.white)
                .multilineTextAlignment(.center)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: 480)
                .accessibilityIdentifier("live.slate.text")
            Button(action: back) {
                Text(verbatim: ParentalWords.text("parental.blocked.back")).frame(minWidth: 160, minHeight: 44)
            }
            .buttonStyle(.bordered)
            .tint(Color.brass)
            .accessibilityIdentifier("live.slate.back")
        }
        .padding(MaktabaTokens.Spacing.s8)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(.black)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("live.slate")
    }
}
