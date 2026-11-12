import MaktabaKit
import SwiftUI

// Epic 57 on Apple TV (stories 57.11 and 57.15): the blocked page, Ask a parent and the
// parent PIN with the remote, the time left, the time's-up screen and the live slate. The
// models and every word but one are MaktabaKit's (`BlockedPage`, `PinEntry`, `TimeLeft`,
// `TimeUp`, `ParentalWords`: English and Arabic under the epic's keys), as on iPhone and
// iPad; these are the TV's own screens for them: big type, focus, the Siri remote.

/// A page built again when the account's limit changes (`ParentalStore.epoch`): it reads the
/// home afresh, so nothing hidden stays on it and what a parent just allowed appears.
struct ParentalFresh<Content: View>: View {
    @Environment(\.appSession) private var session
    @ViewBuilder let content: Content

    var body: some View {
        content.id(session?.parental.epoch ?? 0)
    }
}

extension View {
    func parentalFresh() -> some View { ParentalFresh { self } }
}

/// "This isn't available": a lock, the reason when the home says the account or the device
/// is limited, Ask a parent (focused first), Enter parent PIN, Back. Never a title, a poster
/// or a rating: the home sends none.
struct TVBlockedPage: View {
    let client: APIClient
    let session: AppSession
    /// The line under the title instead of the limit's reason (the player, mid-video).
    var stoppedLine: String?
    let back: () -> Void
    /// A parent opened it (a right PIN): the screen behind reads the home again.
    var opened: () -> Void = {}

    @State private var page: BlockedPage
    @State private var enteringPIN = false
    @FocusState private var focus: Field?
    @Environment(\.scenePhase) private var scenePhase

    private enum Field: Hashable { case ask, check, pin, back }

    init(client: APIClient, session: AppSession, subject: ParentalSubject, stoppedLine: String? = nil,
         back: @escaping () -> Void, opened: @escaping () -> Void = {}) {
        self.client = client
        self.session = session
        self.stoppedLine = stoppedLine
        self.back = back
        self.opened = opened
        _page = State(initialValue: BlockedPage(client: client, subject: subject, limit: session.parental.current))
    }

    var body: some View {
        VStack(spacing: 28) {
            Image(systemName: "lock.fill")
                .font(.system(size: 96))
                .foregroundStyle(Theme.gold)
                .accessibilityLabel(Text(verbatim: ParentalWords.text("parental.blocked.a11y")))
                .accessibilityIdentifier("blocked.lock")
            Text(verbatim: ParentalWords.text("parental.blocked.title"))
                .font(Theme.heading(56))
                .foregroundStyle(Theme.text)
                .multilineTextAlignment(.center)
                .accessibilityAddTraits(.isHeader)
                .accessibilityIdentifier("blocked.title")
            if let line = stoppedLine ?? page.reasonLine {
                Text(verbatim: line)
                    .font(Theme.body(32))
                    .foregroundStyle(Theme.muted)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("blocked.reason")
            }
            buttons
            if let line = page.askLine() {
                Text(verbatim: line)
                    .font(Theme.body(28))
                    .foregroundStyle(Theme.text)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("blocked.askLine")
            }
            if page.canAsk {
                // a TV has no Requests list: a parent answers from their own phone
                Text("tv.parental.askOnPhone", comment: "The blocked page on Apple TV, under Ask a parent")
                    .font(Theme.body(26))
                    .foregroundStyle(Theme.muted)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("blocked.onPhone")
            }
        }
        .padding(80)
        .frame(maxWidth: 1400)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.background.ignoresSafeArea())
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen.blocked")
        .defaultFocus($focus, page.canAsk ? .ask : .back)
        .task { await page.load() }
        // what became of the request for this: the home's list of the child's own requests,
        // read while the app is in front and a request waits (`RequestFollow`)
        .task(id: Following(front: scenePhase == .active, waiting: page.waiting)) {
            guard scenePhase == .active, page.canAsk else { return }
            await page.follow.follow()
        }
        .onChange(of: page.waiting) { _, waiting in
            // the ask button went quiet (Check again takes the focus) or came back (a no:
            // Ask again; the request expired: Ask a parent)
            if waiting {
                focus = page.follow.unsupported ? (page.canEnterPIN ? .pin : .back) : .check
            } else if page.canPressAsk {
                focus = .ask
            }
        }
        // a parent said yes while this page watched: the limit is read and the item opens
        .onChange(of: page.follow.approvals) { _, _ in reopen() }
        // a home from before that list: asked every 30 s whether the limit changed (a parent
        // answered), as before; the page behind is built again when it did (`parentalFresh`)
        .task(id: page.waiting && page.follow.unsupported) {
            guard page.waiting, page.follow.unsupported else { return }
            while !Task.isCancelled {
                do { try await Task.sleep(for: .seconds(30)) } catch { return }
                await session.parental.refresh()
            }
        }
        .fullScreenCover(isPresented: $enteringPIN) {
            TVPinScreen(client: client, subject: page.subject, people: page.unlockers) {
                enteringPIN = false
                reopen()
            }
        }
    }

    /// What the following of a request depends on: the app in front, a request waiting.
    private struct Following: Equatable {
        let front: Bool
        let waiting: Bool
    }

    /// A parent opened it (a PIN, an approval): the limit is read again and the screen
    /// behind loads the item.
    private func reopen() {
        Task {
            await session.parental.refresh()
            opened()
        }
    }

    /// Ask a parent (Ask again after a no), Check again while a request waits, Enter parent
    /// PIN, Back.
    private var buttons: some View {
        VStack(spacing: 24) {
            if page.canAsk {
                Button {
                    Task { await page.askParent() }
                } label: {
                    Text(verbatim: ParentalWords.text(page.askKey("parental.blocked.ask"))).frame(minWidth: 520)
                }
                .disabled(!page.canPressAsk)
                .focused($focus, equals: .ask)
                .accessibilityIdentifier("blocked.ask")
            }
            if page.waiting, !page.follow.unsupported {
                Button {
                    Task { await page.follow.check() }
                } label: {
                    Text(verbatim: ParentalWords.text("parental.ask.check")).frame(minWidth: 520)
                }
                .disabled(page.follow.checking)
                .focused($focus, equals: .check)
                .accessibilityIdentifier("blocked.check")
            }
            if page.canEnterPIN {
                Button { enteringPIN = true } label: {
                    Text(verbatim: ParentalWords.text("parental.blocked.pin")).frame(minWidth: 520)
                }
                .focused($focus, equals: .pin)
                .accessibilityIdentifier("blocked.pin")
            }
            Button(action: back) {
                Text(verbatim: ParentalWords.text("parental.blocked.back")).frame(minWidth: 520)
            }
            .focused($focus, equals: .back)
            .accessibilityIdentifier("blocked.back")
        }
        .buttonStyle(.gold)
        .padding(.top, 12)
        .focusSection()
    }
}

/// Enter parent PIN with the remote: who is typing it (when more than one parent has a PIN),
/// then six digits on a pad of focusable keys. The pad and the dots are left to right in
/// Arabic too, the keys in their phone order (1 2 3 on the top row), and the focus moves over
/// them as they are drawn.
struct TVPinScreen: View {
    let client: APIClient
    let subject: ParentalSubject
    let people: [ParentalPerson]
    let unlocked: () -> Void

    @State private var entry: PinEntry?
    @FocusState private var key: Int?
    @Environment(\.dismiss) private var dismiss

    /// The delete key's place in the focus (the digits are their own).
    private static let delete = -1
    private static let cancel = -2

    var body: some View {
        ZStack {
            Theme.background.ignoresSafeArea()
            if let entry {
                pad(entry)
            } else {
                chooser
            }
        }
        .onAppear {
            // one person with a PIN: no question to ask
            if entry == nil, people.count == 1, let only = people.first { choose(only) }
        }
    }

    private func choose(_ person: ParentalPerson) {
        entry = PinEntry(client: client, subject: subject, guardian: person)
        key = 1
    }

    private var chooser: some View {
        VStack(spacing: 28) {
            Text(verbatim: ParentalWords.text("parental.pin.choose"))
                .font(Theme.heading(48))
                .foregroundStyle(Theme.text)
                .accessibilityAddTraits(.isHeader)
            ForEach(people) { person in
                Button { choose(person) } label: {
                    Label { Text(verbatim: person.username) } icon: { Image(systemName: "person.crop.circle") }
                        .frame(minWidth: 520)
                }
                .accessibilityIdentifier("pin.person.\(person.id)")
            }
            Button { dismiss() } label: {
                Text(verbatim: ParentalWords.text("parental.pin.cancel")).frame(minWidth: 520)
            }
            .accessibilityIdentifier("pin.cancel")
        }
        .buttonStyle(.gold)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("pin.chooser")
    }

    private func pad(_ entry: PinEntry) -> some View {
        VStack(spacing: 24) {
            Text(verbatim: entry.guardian.username)
                .font(Theme.body(28))
                .foregroundStyle(Theme.muted)
            Text(verbatim: ParentalWords.text("parental.pin.enter"))
                .font(Theme.heading(44))
                .foregroundStyle(Theme.text)
                .multilineTextAlignment(.center)
            HStack(spacing: 24) {
                ForEach(0..<PinEntry.length, id: \.self) { i in
                    Circle()
                        .fill(i < entry.digits.count ? Theme.gold : Color.clear)
                        .overlay(Circle().stroke(Theme.muted, lineWidth: 3))
                        .frame(width: 32, height: 32)
                }
            }
            .environment(\.layoutDirection, .leftToRight)
            .accessibilityElement()
            .accessibilityLabel(Text(verbatim: ParentalWords.text("parental.pin.digits", entry.digits.count)))
            .accessibilityIdentifier("pin.dots")
            Group {
                if entry.state == .checking {
                    ProgressView()
                } else if entry.lockedUntil != nil {
                    // PIN entry is closed: the time left counts down as it passes
                    TimelineView(.periodic(from: .now, by: 5)) { timeline in
                        Text(verbatim: entry.line(at: timeline.date) ?? " ")
                            .foregroundStyle(Theme.goldText)
                            .multilineTextAlignment(.center)
                            .accessibilityIdentifier("pin.line")
                    }
                } else if let line = entry.line() {
                    Text(verbatim: line)
                        .foregroundStyle(entry.state == .unlocked ? Theme.text : Theme.goldText)
                        .multilineTextAlignment(.center)
                        .accessibilityIdentifier("pin.line")
                } else {
                    Text(verbatim: " ")
                }
            }
            .font(Theme.body(28, weight: .medium))
            .frame(minHeight: 44)
            keys(entry)
            Button { dismiss() } label: {
                Text(verbatim: ParentalWords.text("parental.pin.cancel")).frame(minWidth: 320)
            }
            .buttonStyle(.gold)
            .focused($key, equals: Self.cancel)
            .accessibilityIdentifier("pin.cancel")
        }
        .padding(60)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("pin.pad")
        .defaultFocus($key, 1)
        .onChange(of: entry.complete) { _, complete in
            if complete { Task { await entry.submit() } }
        }
        // closed after too many tries: the keys open again when the lock ends
        .task(id: entry.lockedUntil) { await entry.reopenWhenLockEnds() }
        .onChange(of: entry.state) { _, state in
            switch state {
            case .unlocked:
                Task {
                    try? await Task.sleep(for: .milliseconds(700))
                    unlocked()
                }
            case .wrong, .failed, .entering:
                // the keys took the remote again: back to the first (from Cancel too, where
                // the focus waited while PIN entry was closed)
                if key == nil || key == Self.cancel { key = 1 }
            case .locked:
                key = Self.cancel
            case .checking:
                break
            }
        }
    }

    /// 1 2 3 / 4 5 6 / 7 8 9 / 0 ⌫, left to right in every language.
    private func keys(_ entry: PinEntry) -> some View {
        VStack(spacing: 20) {
            ForEach(PinPad.rows, id: \.self) { row in
                HStack(spacing: 20) {
                    ForEach(row, id: \.self) { d in digit(d, entry) }
                }
            }
            HStack(spacing: 20) {
                Color.clear.frame(width: TVPinKeyStyle.width, height: TVPinKeyStyle.height)
                digit(0, entry)
                Button { entry.delete() } label: {
                    Image(systemName: "delete.backward")
                }
                .buttonStyle(TVPinKeyStyle())
                .focused($key, equals: Self.delete)
                .disabled(!entry.canType || entry.digits.isEmpty)
                .accessibilityLabel(Text(verbatim: ParentalWords.text("parental.pin.delete")))
                .accessibilityIdentifier("pin.delete")
            }
        }
        .environment(\.layoutDirection, .leftToRight)
        .focusSection()
    }

    private func digit(_ d: Int, _ entry: PinEntry) -> some View {
        Button { entry.press(d) } label: {
            // Western digits on the keys, as a phone's pad shows them in Arabic too
            Text(verbatim: String(d))
        }
        .buttonStyle(TVPinKeyStyle())
        .focused($key, equals: d)
        .disabled(!entry.canType)
        .accessibilityIdentifier("pin.key.\(d)")
    }
}

/// A key of the PIN pad: a raised tile, gold when focused.
struct TVPinKeyStyle: ButtonStyle {
    static let width: CGFloat = 150
    static let height: CGFloat = 96

    func makeBody(configuration: Configuration) -> some View {
        Key(configuration: configuration)
    }

    private struct Key: View {
        let configuration: ButtonStyleConfiguration
        @Environment(\.isFocused) private var focused
        @Environment(\.isEnabled) private var enabled

        var body: some View {
            configuration.label
                .font(.system(size: 44, weight: .medium, design: .rounded))
                .foregroundStyle(focused ? Theme.onGold : Theme.text)
                .frame(width: TVPinKeyStyle.width, height: TVPinKeyStyle.height)
                .background(focused ? Theme.gold : Theme.raised, in: .rect(cornerRadius: 20))
                .overlay(RoundedRectangle(cornerRadius: 20).strokeBorder(Theme.border, lineWidth: focused ? 0 : 1))
                .shadow(color: focused ? Theme.goldText.opacity(0.5) : .clear, radius: 16)
                .scaleEffect(configuration.isPressed ? 0.96 : focused ? 1.08 : 1)
                .opacity(enabled ? 1 : 0.45)
                .animation(.easeOut(duration: 0.12), value: focused)
        }
    }
}

// MARK: time (57.15)

/// "Time's up for today" (or "Not watching time yet"): when watching opens again, where the
/// video stopped, Ask for more time (15, 30 or 60 minutes), Enter parent PIN, Back. Playback
/// stopped and is not tried again from here: the home is asked every 30 s whether there is
/// time again (a parent gave some), and only then does the video go on.
struct TVTimeUpView: View {
    let client: APIClient
    let session: AppSession
    let up: TimeUp
    /// Where the video stopped, in seconds (nil: nothing was playing).
    var position: Double?
    /// The library of what was playing: its own time rules count too.
    var library: String?
    let back: () -> Void
    /// The home allows watching again.
    var allowed: () -> Void = {}

    @State private var minutes = 30
    @State private var page: BlockedPage?
    @State private var asked: BlockedPage?
    @State private var enteringPIN = false
    @FocusState private var focus: Field?
    @Environment(\.scenePhase) private var scenePhase

    private enum Field: Hashable { case minutes(Int), ask, check, pin, back }

    var body: some View {
        VStack(spacing: 24) {
            Image(systemName: "hourglass.bottomhalf.filled")
                .font(.system(size: 88))
                .foregroundStyle(Theme.gold)
                .accessibilityHidden(true)
            Text(verbatim: up.title())
                .font(Theme.heading(56))
                .foregroundStyle(Theme.text)
                .multilineTextAlignment(.center)
                .accessibilityAddTraits(.isHeader)
                .accessibilityIdentifier("timeUp.title")
            if let next = up.nextLine() {
                Text(verbatim: next)
                    .font(Theme.body(32))
                    .foregroundStyle(Theme.text)
                    .multilineTextAlignment(.center)
                    .accessibilityIdentifier("timeUp.next")
            }
            if let position, position >= 1 {
                Text(verbatim: ParentalWords.text("parental.time.saved", Self.clock(position)))
                    .font(Theme.body(28))
                    .foregroundStyle(Theme.muted)
                    .accessibilityIdentifier("timeUp.saved")
            }
            Text(verbatim: ParentalWords.text("parental.time.askParent"))
                .font(Theme.body(28))
                .foregroundStyle(Theme.muted)
                .multilineTextAlignment(.center)
                .accessibilityIdentifier("timeUp.ask")
            if let page, page.canAsk { asking(page) }
            Button(action: back) {
                Text(verbatim: ParentalWords.text("parental.blocked.back")).frame(minWidth: 520)
            }
            .focused($focus, equals: .back)
            .accessibilityIdentifier("timeUp.back")
        }
        .buttonStyle(.gold)
        .padding(60)
        .frame(maxWidth: 1400)
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .background(Theme.background.ignoresSafeArea())
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("screen.timeUp")
        .defaultFocus($focus, .back)
        .task {
            let p = BlockedPage(client: client, subject: .time(minutes: minutes), limit: session.parental.current)
            await p.load()
            page = p
            if p.canAsk { focus = .ask }
        }
        .onChange(of: asked?.ask) { _, ask in
            // the button went quiet: Back takes the focus
            if ask == .tooMany { focus = .back }
        }
        // what became of the request for more time: the home's list of the child's own
        // requests, read while the app is in front and a request waits (`RequestFollow`)
        .task(id: Following(front: scenePhase == .active, ready: page != nil, waiting: page?.waiting ?? false)) {
            guard scenePhase == .active, let page, page.canAsk else { return }
            await page.follow.follow()
        }
        .onChange(of: page?.waiting ?? false) { _, waiting in
            // the ask button went quiet (Check again takes the focus) or came back (a no:
            // Ask again; the request expired)
            guard let page else { return }
            if waiting {
                focus = page.follow.unsupported ? .back : .check
            } else if page.canPressAsk {
                focus = .ask
            }
        }
        // a parent said yes while this screen watched: the home's time is read once, and the
        // video goes on when it says there is time
        .onChange(of: page?.follow.approvals ?? 0) { _, _ in
            Task {
                guard let t = await PlayingTime(source: client, library: library).ask(), t.allowed else { return }
                await session.parental.refreshTime()
                allowed()
            }
        }
        .fullScreenCover(isPresented: $enteringPIN) {
            TVPinScreen(client: client, subject: .time(minutes: minutes), people: page?.unlockers ?? []) {
                enteringPIN = false
                Task {
                    await session.parental.refreshTime()
                    allowed()
                }
            }
        }
        // never at once: the home just said no. Asked again every 30 s, for the library of
        // what was playing (`PlayingTime`, as on iPhone), and the video goes on only when the
        // home says there is time
        .task {
            guard await PlayingTime(source: client, library: library).waitUntilAllowed() else { return }
            await session.parental.refreshTime()
            allowed()
        }
    }

    /// What the following of a request depends on: the app in front, the page read, a
    /// request waiting.
    private struct Following: Equatable {
        let front: Bool
        let ready: Bool
        let waiting: Bool
    }

    /// Ask for more time: how much (the one chosen is marked, and is what Ask and the PIN
    /// give), the button (Ask again after a no), Check again while the request waits, Enter
    /// parent PIN, and what became of the request.
    @ViewBuilder private func asking(_ page: BlockedPage) -> some View {
        HStack(spacing: 24) {
            ForEach(ParentalSubject.timeChoices, id: \.self) { m in
                Button { minutes = m } label: {
                    Label {
                        Text(verbatim: ParentalWords.length(seconds: m * 60))
                    } icon: {
                        Image(systemName: m == minutes ? "checkmark.circle.fill" : "circle")
                    }
                }
                .focused($focus, equals: .minutes(m))
                .accessibilityAddTraits(m == minutes ? .isSelected : [])
                .accessibilityIdentifier("timeUp.minutes.\(m)")
            }
        }
        .focusSection()
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("timeUp.minutes")
        Button {
            let ask = page.asking(.time(minutes: minutes))
            asked = ask
            Task { await ask.askParent() }
        } label: {
            Text(verbatim: ParentalWords.text(page.askKey("parental.time.askMore"))).frame(minWidth: 520)
        }
        .disabled(!(asked ?? page).canPressAsk)
        .focused($focus, equals: .ask)
        .accessibilityIdentifier("timeUp.askMore")
        if page.waiting, !page.follow.unsupported {
            Button {
                Task { await page.follow.check() }
            } label: {
                Text(verbatim: ParentalWords.text("parental.ask.check")).frame(minWidth: 520)
            }
            .disabled(page.follow.checking)
            .focused($focus, equals: .check)
            .accessibilityIdentifier("timeUp.check")
        }
        if page.canEnterPIN {
            Button { enteringPIN = true } label: {
                Text(verbatim: ParentalWords.text("parental.blocked.pin")).frame(minWidth: 520)
            }
            .focused($focus, equals: .pin)
            .accessibilityIdentifier("timeUp.pin")
        }
        if let line = (asked ?? page).askLine() {
            Text(verbatim: line)
                .font(Theme.body(26))
                .foregroundStyle(Theme.text)
                .multilineTextAlignment(.center)
                .accessibilityIdentifier("timeUp.askLine")
        }
    }

    /// A position as a clock ("12:05", "1:02:03"), digits left to right.
    static func clock(_ seconds: Double) -> String {
        "\u{200E}" + Duration.seconds(seconds.rounded(.down)).formatted(.time(pattern: seconds >= 3600 ? .hourMinuteSecond : .minuteSecond))
    }
}

/// The time left today, on Home and over the player, in the last 15 minutes; the warnings at
/// 5 minutes and at 1 minute, shown for a moment; and, on Home, that the time is over. All
/// from the home's answer (`TimeLeft`): the TV counts nothing of its own.
struct TVTimeLeft: View {
    let store: ParentalStore
    /// Over the video: light text on a dark capsule, and the home asked again every 30 s.
    var overVideo = false
    /// The player's: who asks, and the playing video's library (a rule on one library counts
    /// only when the home is told the library).
    var client: APIClient?
    var library: String?

    @State private var warning: Int?
    @State private var last: Int?
    @State private var time: PlayingTime?

    /// The answer shown: the playing library's once the home gave it.
    private var shown: ParentalStore.TimeReading? { overVideo ? time?.reading ?? store.time : store.time }

    var body: some View {
        if store.current.restricted, let reading = store.time {
            if !overVideo, let up = TimeUp(reading.state) {
                VStack(alignment: .leading, spacing: 6) {
                    Text(verbatim: up.title()).font(Theme.body(30, weight: .semibold))
                    if let next = up.nextLine() {
                        Text(verbatim: next).font(Theme.body(26)).foregroundStyle(Theme.muted)
                    }
                }
                .foregroundStyle(Theme.text)
                .padding(.horizontal, 32)
                .padding(.vertical, 16)
                .background(Theme.surface, in: .rect(cornerRadius: Theme.cornerRadius))
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("time.up.banner")
            } else {
                countdown
            }
        }
    }

    private var countdown: some View {
        TimelineView(.periodic(from: .now, by: 5)) { timeline in
            let left = TimeLeft.seconds(shown, at: timeline.date)
            Group {
                if let warning {
                    label(TimeLeft.warningLine(warning), strong: true)
                        .accessibilityIdentifier("time.warning")
                } else if let line = TimeLeft.line(shown, at: timeline.date) {
                    label(line, strong: false)
                        .accessibilityIdentifier("time.left")
                }
            }
            .onChange(of: left, initial: true) { before, now in
                if let due = TimeLeft.warning(from: last ?? before, to: now), due != warning { warn(due) }
                last = now
            }
        }
        .task(id: library) {
            guard overVideo else { return }
            guard let client else {
                // no client given: Home's question, as before
                while !Task.isCancelled {
                    await store.refreshTime()
                    do { try await Task.sleep(for: PlayingTime.every) } catch { return }
                }
                return
            }
            let t = PlayingTime(source: client, library: library)
            time = t
            await t.follow()
        }
        .onDisappear {
            // Home's line (asked without a library) is read again when the player closes
            if overVideo { Task { await store.refreshTime() } }
        }
    }

    private func warn(_ threshold: Int) {
        warning = threshold
        UIAccessibility.post(notification: .announcement, argument: TimeLeft.warningLine(threshold))
        Task {
            try? await Task.sleep(for: .seconds(8))
            if warning == threshold { warning = nil }
        }
    }

    private func label(_ text: String, strong: Bool) -> some View {
        IconLine(text, systemImage: "hourglass")
            .font(Theme.body(28, weight: strong ? .semibold : .medium))
            .foregroundStyle(overVideo ? Color.white : Theme.text)
            .padding(.horizontal, 32)
            .padding(.vertical, 14)
            .background(overVideo ? AnyShapeStyle(.black.opacity(0.65)) : AnyShapeStyle(Theme.surface), in: .capsule)
            .accessibilityElement(children: .combine)
    }
}

/// What a player shows when the home stopped the video for a parental reason: the time's-up
/// screen or the blocked page, in place of the picture.
struct TVParentalStopView: View {
    let playback: TVPlayback
    let stop: TVPlayback.ParentalStop
    let session: AppSession
    let back: () -> Void

    var body: some View {
        switch stop {
        case let .time(up, position):
            TVTimeUpView(client: playback.client, session: session, up: up, position: position,
                         library: playback.video.libraryID, back: back) { playback.resumeAfterStop() }
        case let .blocked(midPlay):
            TVBlockedPage(client: playback.client, session: session, subject: .video(playback.video.id),
                          stoppedLine: midPlay ? ParentalWords.text("parental.player.stopped") : nil,
                          back: back) { playback.resumeAfterStop() }
        }
    }
}
