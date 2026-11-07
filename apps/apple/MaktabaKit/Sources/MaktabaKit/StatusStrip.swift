import SwiftUI

/// The status strip (Epic 53 story 53.06): the highest line of what is wrong on the home,
/// "N more" for the rest, Close (it comes back when the state changes), and for an admin
/// Details, which opens the home's System health or recovery page. On tvOS it is a top
/// strip that does not take focus.
public struct StatusStripView: View {
    /// A line's words in the person's language, with its values (the cloud's maintenance window).
    private func words(_ line: StripLine) -> Text {
        if line.args.isEmpty { return Text(LocalizedStringKey(line.text), bundle: .module) }
        return Text(verbatim: String(format: StripWords.localized(line.text), arguments: line.args))
    }

    let lines: [StripLine]
    let home: URL?
    let close: () -> Void
    @State private var open = false
    @Environment(\.openURL) private var openURL

    public init(lines: [StripLine], home: URL?, close: @escaping () -> Void) {
        self.lines = lines
        self.home = home
        self.close = close
    }

    public var body: some View {
        if let first = lines.first {
            HStack(alignment: .firstTextBaseline, spacing: 10) {
                Image(systemName: first.key == "offline" ? "wifi.slash" : "exclamationmark.triangle.fill")
                    .foregroundStyle(.orange)
                VStack(alignment: .leading, spacing: 4) {
                    ForEach(open ? lines : [first]) { line in
                        HStack(alignment: .firstTextBaseline, spacing: 8) {
                            words(line)
                                .fixedSize(horizontal: false, vertical: true)
                            #if !os(tvOS)
                            if let url = line.link ?? line.details.flatMap({ link in home.flatMap { URL(string: link, relativeTo: $0) } }) {
                                Button { openURL(url) } label: { Text("Details", bundle: .module) }
                                    .buttonStyle(.borderless)
                            }
                            #endif
                        }
                        .accessibilityIdentifier("strip.\(line.key)")
                    }
                    #if !os(tvOS)
                    if lines.count > 1 && !open {
                        Button { open = true } label: { Text("\(lines.count - 1) more", bundle: .module) }
                            .buttonStyle(.borderless)
                    }
                    #endif
                }
                .frame(maxWidth: .infinity, alignment: .leading)
                #if !os(tvOS)
                Button(action: close) { Image(systemName: "xmark") }
                    .buttonStyle(.borderless)
                    .accessibilityLabel(Text("Close", bundle: .module))
                #endif
            }
            .font(.callout)
            .padding(12)
            .background(.regularMaterial, in: .rect(cornerRadius: 12))
            .padding(.horizontal, 12)
            .padding(.vertical, 6)
            #if os(tvOS)
            .focusable(false)
            #endif
            .accessibilityElement(children: .contain)
            .accessibilityIdentifier("status.strip")
        }
    }
}

/// `.homeStatusStrip(client:admin:)`: the strip at the top of a signed-in screen, its status
/// read every 10 s while the app is in front.
public struct HomeStatusStrip: ViewModifier {
    let client: APIClient
    let home: URL?
    let admin: Bool
    let liveTVOff: Bool
    @State private var watcher = HomeStatusWatcher()
    @State private var cloud = CloudStatusWatcher()
    @Environment(\.scenePhase) private var scenePhase

    public func body(content: Content) -> some View {
        // Maktaba Cloud's line only when the home is reached through the cloud (story 50.11)
        let viaCloud = CloudStatusRules.throughCloud(home ?? client.home)
        let lines = StatusStripRules.lines(watcher.status, online: true, admin: admin, liveTVOff: liveTVOff,
                                           cloud: viaCloud ? CloudStatusRules.line(cloud.summary) : nil)
        let sig = StatusStripRules.signature(lines)
        content
            .safeAreaInset(edge: .top, spacing: 0) {
                if !lines.isEmpty && watcher.closed != sig {
                    StatusStripView(lines: lines, home: home) { watcher.closed = sig }
                }
            }
            .environment(\.homeStatus, watcher.status)
            .task(id: scenePhase == .active) {
                guard scenePhase == .active else { return }
                await watcher.run(client)
            }
            .task(id: scenePhase == .active && viaCloud) {
                guard scenePhase == .active && viaCloud else { return }
                await cloud.run()
            }
    }
}

extension View {
    /// The status strip of the home `client` reaches (Epic 53 story 53.06).
    public func homeStatusStrip(client: APIClient, home: URL? = nil, admin: Bool, liveTVOff: Bool = false) -> some View {
        modifier(HomeStatusStrip(client: client, home: home, admin: admin, liveTVOff: liveTVOff))
    }
}

private struct HomeStatusKey: EnvironmentKey {
    static let defaultValue: HomeStatusDoc? = nil
}

extension EnvironmentValues {
    /// The home's status as the strip last read it (nil: not known): the player's error
    /// screen says playback is unavailable instead of a generic error when streaming is down.
    public var homeStatus: HomeStatusDoc? {
        get { self[HomeStatusKey.self] }
        set { self[HomeStatusKey.self] = newValue }
    }
}
