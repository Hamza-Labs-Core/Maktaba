import SwiftUI

// The screens every app shares for Epic 53's crash reports (53.14) and problem reports
// (53.15): the one-time question and the Settings → Privacy switch, the Report a problem
// form, My reports, and the hook an error screen's "Report this" opens the form through.

// MARK: words

/// The words of these screens, in MaktabaKit's catalog (English and Arabic).
public enum ReportWords {
    public static func category(_ c: ReportCategory) -> String {
        switch c {
        case .playback: kit("Playback")
        case .liveTV: kit("Live TV")
        case .search: kit("Search")
        case .signingIn: kit("Signing in")
        case .other: kit("Something else")
        }
    }

    public static func category(_ raw: String) -> String {
        ReportCategory(rawValue: raw).map(category) ?? raw
    }

    public static func status(_ s: ReportStatus) -> String {
        switch s {
        case .new: kit("New")
        case .looking: kit("Looking into it")
        case .fixed: kit("Fixed")
        case .closed: kit("Closed")
        }
    }

    public static func status(_ raw: String) -> String {
        ReportStatus(rawValue: raw).map(status) ?? raw
    }

    public static var title: String { kit("Report a problem") }
    public static var mine: String { kit("My reports") }
    public static var thisError: String { kit("Report this") }
    public static func crashSetting(_ home: String) -> String { kit("Send crash reports to \(home)") }
    public static func crashSettingHelp(_ home: String) -> String { kit("Off by default. When on, a report goes to \(home) after a crash, never to Maktaba.") }
}

// MARK: crash reports: the question, the switch, sending

/// The one-time question after signing in (53.14 §3): two equal buttons, neither chosen
/// for the person.
public struct CrashQuestionView: View {
    let home: String
    let answer: (Bool) -> Void

    public init(home: String, answer: @escaping (Bool) -> Void) {
        self.home = home
        self.answer = answer
    }

    public var body: some View {
        VStack(spacing: 24) {
            Image(systemName: "stethoscope")
                .font(.system(size: 44))
                .foregroundStyle(Color.brass)
                .accessibilityHidden(true)
            Text(verbatim: kit("Help \(home) fix problems?"))
                .font(.title2.bold())
                .multilineTextAlignment(.center)
                .accessibilityIdentifier("crash.question.title")
            Text(verbatim: kit("If the app crashes, it can send a crash report to \(home) so its admin can fix it. It contains no videos or messages. It is off unless you turn it on."))
                .multilineTextAlignment(.center)
                .foregroundStyle(.secondary)
                .fixedSize(horizontal: false, vertical: true)
            // the same style and size, one above the other: neither is the default
            VStack(spacing: 12) { buttons }
        }
        .padding(32)
        .frame(maxWidth: 640)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("crash.question")
    }

    @ViewBuilder private var buttons: some View {
        Button { answer(true) } label: {
            Text(verbatim: kit("Send crash reports")).frame(maxWidth: .infinity)
        }
        .buttonStyle(.bordered)
        .accessibilityIdentifier("crash.question.yes")
        Button { answer(false) } label: {
            Text(verbatim: kit("Not now")).frame(maxWidth: .infinity)
        }
        .buttonStyle(.bordered)
        .accessibilityIdentifier("crash.question.no")
    }
}

/// Settings → Privacy: "Send crash reports to {home}", off until the person turns it on.
public struct CrashReportsToggle: View {
    let session: AppSession
    @State private var on = false

    public init(session: AppSession) { self.session = session }

    public var body: some View {
        if let home = session.current {
            VStack(alignment: .leading, spacing: 4) {
                Toggle(isOn: Binding(get: { on }, set: { v in
                    on = v
                    CrashConsent().set(v, for: home)
                    if v, let client = session.client { Task { await CrashOutbox.shared?.flush(to: client, home: home) } }
                })) {
                    Text(verbatim: ReportWords.crashSetting(home.name))
                }
                .accessibilityIdentifier("crash.setting")
                Text(verbatim: ReportWords.crashSettingHelp(home.name))
                    .font(.footnote)
                    .foregroundStyle(.secondary)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .onAppear { on = CrashConsent().isOn(home) }
        }
    }
}

/// Asks the crash-report question once per home after signing in, sends what is due when
/// the person said yes (now, and whenever MetricKit hands over a crash), and says once what
/// happened to the last launch's crash.
struct CrashReportsModifier: ViewModifier {
    let session: AppSession
    @State private var asking = false
    @State private var notice: CrashOutbox.Notice?

    func body(content: Content) -> some View {
        content
            .task(id: session.current?.url) { await start() }
            .onReceive(NotificationCenter.default.publisher(for: CrashReports.didStore)) { _ in
                Task { await send() }
            }
            .sheet(isPresented: $asking) {
                CrashQuestionView(home: session.current?.name ?? "") { yes in
                    if let home = session.current { CrashConsent().set(yes, for: home) }
                    asking = false
                    Task { await send() }
                }
                .interactiveDismissDisabled()
            }
            .alert(Text(verbatim: noticeText), isPresented: Binding(get: { notice != nil }, set: { if !$0 { notice = nil } })) {
                Button(role: .cancel) { notice = nil } label: { Text(verbatim: kit("OK")) }
            }
    }

    private var noticeText: String {
        let home = session.current?.name ?? ""
        return notice == .sent
            ? kit("The app closed unexpectedly last time. A report was sent to \(home).")
            : kit("The app closed unexpectedly last time. The crash is kept on this device; you can attach it to a problem report.")
    }

    private func start() async {
        guard let home = session.current, session.signedIn else { return }
        if CrashConsent().answer(home) == nil {
            if CrashConsent.questionAllowed() { asking = true }
            return
        }
        await send()
    }

    private func send() async {
        guard let home = session.current, let client = session.client, let outbox = CrashOutbox.shared else { return }
        await outbox.flush(to: client, home: home)
        if !asking, CrashConsent().answer(home) != nil, CrashConsent.questionAllowed() { notice = outbox.notice() }
    }
}

public extension View {
    /// Crash reports to the signed-in home (53.14): the question, sending, the notice.
    func crashReports(session: AppSession) -> some View { modifier(CrashReportsModifier(session: session)) }
}

// MARK: Report a problem

/// What opens the Report a problem form, with the error screen's reference when there is one.
public struct ReportProblemAction: Sendable {
    let open: @MainActor @Sendable (String?) -> Void

    @MainActor public func callAsFunction(reference: String? = nil) { open(reference) }
}

public extension EnvironmentValues {
    /// Set by `reportsProblems(session:)`: error screens show "Report this" when it is.
    @Entry var reportProblem: ReportProblemAction?
}

struct ReportsProblemsModifier: ViewModifier {
    let session: AppSession
    @State private var open: Opened?

    struct Opened: Identifiable {
        let id = UUID()
        let reference: String?
        let screenshot: Data?
    }

    func body(content: Content) -> some View {
        content
            .environment(\.reportProblem, ReportProblemAction { ref in
                // the screen as it was before the form covers it
                open = Opened(reference: ref, screenshot: ScreenCapture.window())
            })
            .sheet(item: $open) { o in
                if let client = session.client {
                    NavigationStack {
                        ReportProblemView(client: client, home: session.current?.name ?? "", reference: o.reference, screenshot: o.screenshot)
                    }
                }
            }
    }
}

public extension View {
    /// Report a problem from anywhere under this view (Settings, error screens).
    func reportsProblems(session: AppSession) -> some View { modifier(ReportsProblemsModifier(session: session)) }
}

/// The form (53.15 §2): a category, what happened, when, and what will be sent, previewed.
public struct ReportProblemView: View {
    let client: APIClient
    let home: String
    let screenshot: Data?

    @State private var draft: ReportDraft
    @State private var lines: [String] = []
    @State private var crashCount = 0
    @State private var sending = false
    @State private var number: Int?
    @State private var problem: String?
    @State private var showLog = false
    @Environment(\.dismiss) private var dismiss

    public init(client: APIClient, home: String, reference: String? = nil, screenshot: Data? = nil) {
        self.client = client
        self.home = home
        self.screenshot = screenshot
        _draft = State(initialValue: ReportDraft(reference: reference))
    }

    public var body: some View {
        Group {
            if let number {
                sent(number)
            } else {
                form
            }
        }
        .navigationTitle(Text(verbatim: ReportWords.title))
        .toolbar {
            #if !os(tvOS)
            ToolbarItem(placement: .cancellationAction) {
                Button { dismiss() } label: { Text(verbatim: number == nil ? kit("Cancel") : kit("Done")) }
                    .accessibilityIdentifier("report.close")
            }
            #endif
        }
        .task {
            lines = ReportProblem.lines(LogRing.shared?.lines() ?? [])
            crashCount = CrashOutbox.shared?.files().count ?? 0
        }
    }

    private var form: some View {
        Form {
            Section {
                Picker(selection: $draft.category) {
                    ForEach(ReportCategory.allCases, id: \.self) { Text(verbatim: ReportWords.category($0)).tag($0) }
                } label: {
                    Text(verbatim: kit("Category"))
                }
                .accessibilityIdentifier("report.category")
                TextField(text: Binding(get: { draft.text }, set: { draft.text = String($0.prefix(ReportProblem.textMax)) }),
                          axis: .vertical) {
                    Text(verbatim: kit("Describe it (optional)"))
                }
                .lineLimit(3...8)
                .accessibilityIdentifier("report.text")
            } header: {
                Text(verbatim: kit("What happened?"))
            }
            #if !os(tvOS)
            Section {
                Picker(selection: Binding(get: { draft.earlier != nil }, set: { draft.earlier = $0 ? Date().addingTimeInterval(-3600) : nil })) {
                    Text(verbatim: kit("Just now")).tag(false)
                    Text(verbatim: kit("Earlier today")).tag(true)
                } label: {
                    Text(verbatim: kit("When?"))
                }
                .accessibilityIdentifier("report.when")
                if let earlier = draft.earlier {
                    DatePicker(selection: Binding(get: { earlier }, set: { draft.earlier = $0 }),
                               in: Calendar.current.startOfDay(for: Date())...Date(), displayedComponents: .hourAndMinute) {
                        Text(verbatim: kit("Time"))
                    }
                }
            }
            #endif
            whatIsSent
            Section {
                Text(verbatim: kit("Send to the admin of \(home)"))
                    .foregroundStyle(.secondary)
                if let problem {
                    Label { Text(verbatim: problem) } icon: { Image(systemName: "exclamationmark.triangle") }
                        .foregroundStyle(.red)
                        .accessibilityIdentifier("report.problem")
                }
                Button {
                    Task { await send() }
                } label: {
                    if sending { ProgressView() } else { Text(verbatim: kit("Send")).bold() }
                }
                .disabled(sending)
                .accessibilityIdentifier("report.send")
            }
        }
        .maktabaSurface()
        .accessibilityIdentifier("screen.report")
    }

    private var header: Envelope.Header { .current() }

    @ViewBuilder private var whatIsSent: some View {
        Section {
            VStack(alignment: .leading, spacing: 2) {
                Text(verbatim: kit("App version, device model and system version"))
                Text(verbatim: "\(header.app) \(header.appVersion) · \(header.deviceModel) · \(header.osVersion)")
                    .font(.caption.monospaced())
                    .foregroundStyle(.secondary)
                    .environment(\.layoutDirection, .leftToRight)
            }
            #if os(tvOS)
            NavigationLink {
                LogPreview(lines: lines)
            } label: {
                LabeledContent { Text(verbatim: kit("Show")) } label: {
                    Text(verbatim: kit("This app's log for the last 30 minutes (\(lines.count) lines)"))
                }
            }
            .accessibilityIdentifier("report.log")
            #else
            DisclosureGroup(isExpanded: $showLog) {
                LogPreview(lines: lines).frame(height: 260)
            } label: {
                Text(verbatim: kit("This app's log for the last 30 minutes (\(lines.count) lines)"))
            }
            .accessibilityIdentifier("report.log")
            #endif
            Text(verbatim: kit("The last \(min(20, RequestID.recent().count)) requests to the home"))
            if crashCount > 0 {
                Toggle(isOn: $draft.includeCrashes) {
                    Text(verbatim: kit("Include recent crashes of this app (\(crashCount))"))
                }
                .accessibilityIdentifier("report.crashes")
            }
            if screenshot != nil {
                Toggle(isOn: $draft.includeScreenshot) {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(verbatim: kit("Include a screenshot"))
                        Text(verbatim: kit("Off unless you tick it. You'll see the screenshot here before sending."))
                            .font(.footnote)
                            .foregroundStyle(.secondary)
                    }
                }
                .accessibilityIdentifier("report.screenshot")
                if draft.includeScreenshot, let shot = screenshot, let image = Self.image(shot) {
                    image
                        .resizable()
                        .scaledToFit()
                        .frame(maxWidth: .infinity)
                        .accessibilityIdentifier("report.screenshot.preview")
                    Button(role: .destructive) {
                        draft.includeScreenshot = false
                    } label: {
                        Text(verbatim: kit("Remove screenshot"))
                    }
                    .accessibilityIdentifier("report.screenshot.remove")
                }
            }
        } header: {
            Text(verbatim: kit("What will be sent"))
        }
    }

    static func image(_ data: Data) -> Image? {
        #if canImport(UIKit)
        UIImage(data: data).map { Image(uiImage: $0) }
        #elseif canImport(AppKit)
        NSImage(data: data).map { Image(nsImage: $0) }
        #else
        nil
        #endif
    }

    private func sent(_ n: Int) -> some View {
        VStack(spacing: 20) {
            Image(systemName: "checkmark.circle")
                .font(.system(size: 48))
                .foregroundStyle(.green)
                .accessibilityHidden(true)
            Text(verbatim: kit("Sent to \(home) (#\(n))"))
                .font(.title3.bold())
                .multilineTextAlignment(.center)
                .accessibilityIdentifier("report.sent")
            Text(verbatim: kit("The admin's reply will show in My reports."))
                .foregroundStyle(.secondary)
                .multilineTextAlignment(.center)
            Button { dismiss() } label: { Text(verbatim: kit("Done")) }
                .accessibilityIdentifier("report.done")
        }
        .padding(32)
    }

    private func send() async {
        sending = true
        problem = nil
        defer { sending = false }
        do {
            number = try await ReportProblem.send(draft, screenshot: screenshot, client: client)
        } catch let APIError.status(429, _, _) {
            problem = kit("You've sent many reports today. Try again tomorrow.")
        } catch {
            problem = HomeFailure(error).message()
        }
    }
}

/// The lines the report sends, as they will be sent (scrubbed), left to right in every
/// language.
struct LogPreview: View {
    let lines: [String]

    var body: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 4) {
                if lines.isEmpty {
                    Text(verbatim: kit("No lines in the last 30 minutes.")).foregroundStyle(.secondary)
                }
                ForEach(Array(lines.enumerated()), id: \.offset) { _, l in
                    Text(verbatim: l)
                        .font(.caption2.monospaced())
                        .textSelection(.enabledIfAvailable)
                        .frame(maxWidth: .infinity, alignment: .leading)
                }
            }
            .environment(\.layoutDirection, .leftToRight)
        }
        .accessibilityIdentifier("report.preview")
    }
}

extension View {
    @ViewBuilder func textSelection(_ s: TextSelectionAvailability) -> some View {
        #if os(tvOS)
        self
        #else
        switch s {
        case .enabledIfAvailable: textSelection(.enabled)
        }
        #endif
    }
}

enum TextSelectionAvailability { case enabledIfAvailable }

/// My reports (53.15 §5): the person's own reports, their status and the admin's reply.
public struct MyReportsView: View {
    let client: APIClient
    @State private var reports: [ReportSummary]?
    @State private var problem: String?

    public init(client: APIClient) { self.client = client }

    public var body: some View {
        List {
            if let problem {
                Label { Text(verbatim: problem) } icon: { Image(systemName: "exclamationmark.triangle") }
                    .foregroundStyle(.red)
            }
            if let reports, reports.isEmpty {
                Text(verbatim: kit("You haven't sent a report.")).foregroundStyle(.secondary)
                    .accessibilityIdentifier("reports.none")
            }
            ForEach(reports ?? []) { r in
                VStack(alignment: .leading, spacing: 4) {
                    HStack {
                        Text(verbatim: "#\(r.id) · \(ReportWords.category(r.category))").font(.headline)
                        Spacer()
                        Text(verbatim: ReportWords.status(r.status))
                            .font(.caption.bold())
                            .padding(.horizontal, 8).padding(.vertical, 2)
                            .background(Capsule().fill(Color.brass.opacity(0.2)))
                    }
                    if let at = r.at {
                        Text(at.formatted(date: .abbreviated, time: .shortened)).font(.caption).foregroundStyle(.secondary)
                    }
                    if let reply = r.reply, !reply.isEmpty {
                        Text(verbatim: kit("Reply: \(reply)")).font(.callout)
                    }
                }
                .accessibilityElement(children: .combine)
                .accessibilityIdentifier("reports.mine.\(r.id)")
            }
        }
        .maktabaSurface()
        .overlay { if reports == nil, problem == nil { ProgressView() } }
        .navigationTitle(Text(verbatim: ReportWords.mine))
        .task { await load() }
        #if !os(tvOS)
        .refreshable { await load() }
        #endif
    }

    private func load() async {
        do {
            reports = try await client.problemReports()
            problem = nil
        } catch {
            problem = HomeFailure(error).message()
        }
    }
}
