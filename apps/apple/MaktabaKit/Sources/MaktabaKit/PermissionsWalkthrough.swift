import SwiftUI
#if canImport(UIKit)
import UIKit
#elseif canImport(AppKit)
import AppKit
#endif

public extension PermissionKind {
    /// Where the person changes it. iOS and iPadOS: the app's own page in Settings (it has
    /// the Local Network switch once the app has asked). macOS: Privacy & Security → Local
    /// Network in System Settings. tvOS asks for nothing.
    var settingsURL: URL? {
        #if os(iOS)
        URL(string: UIApplication.openSettingsURLString)
        #elseif os(macOS)
        URL(string: "x-apple.systempreferences:com.apple.preference.security?Privacy_LocalNetwork")
        #else
        nil
        #endif
    }
}

public extension View {
    /// Shows the permissions walkthrough over the app: on the first run, and again whenever
    /// a required permission is refused; re-checked each time the app comes to the
    /// foreground. `appName` is the name the person sees in Settings.
    func permissionsWalkthrough(appName: String) -> some View {
        modifier(PermissionsGate(appName: appName))
    }
}

struct PermissionsGate: ViewModifier {
    let appName: String
    @State private var model = PermissionsModel.forThisApp()
    @Environment(\.scenePhase) private var phase

    func body(content: Content) -> some View {
        content
            .sheet(isPresented: $model.isPresented, onDismiss: { model.dismiss() }) {
                PermissionsWalkthrough(model: model, appName: appName)
                    #if os(macOS)
                    .frame(minWidth: 520, minHeight: 440)
                    #endif
            }
            .task { await model.refresh() }
            .onChange(of: phase) { _, now in
                if now == .active { Task { await model.refresh() } }
            }
            #if os(macOS)
            .onReceive(NotificationCenter.default.publisher(for: NSApplication.didBecomeActiveNotification)) { _ in
                Task { await model.refresh() }
            }
            #endif
    }
}

/// Each permission the app needs: why, where it stands, and a button that asks or opens the
/// settings pane. English or Arabic (right to left) after the person's first language.
public struct PermissionsWalkthrough: View {
    let model: PermissionsModel
    let appName: String
    let words: PermissionsStrings
    @Environment(\.openURL) private var openURL

    public init(model: PermissionsModel, appName: String, words: PermissionsStrings = .forPreferredLanguages()) {
        self.model = model
        self.appName = appName
        self.words = words
    }

    public var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            ScrollView {
                VStack(alignment: .leading, spacing: 20) {
                    Text(words.text(.title))
                        .font(.largeTitle.bold())
                        .accessibilityAddTraits(.isHeader)
                        .accessibilityIdentifier("permissions.title")
                    Text(words.text(model.items.isEmpty ? .introNone : .intro, appName))
                        .foregroundStyle(.secondary)
                    ForEach(model.items) { item in
                        PermissionCard(
                            item: item,
                            busy: model.busy.contains(item.kind),
                            words: words,
                            appName: appName,
                            ask: { Task { await model.request(item.kind) } },
                            openSettings: { if let url = item.kind.settingsURL { openURL(url) } }
                        )
                    }
                }
                .padding(24)
                .frame(maxWidth: 640, alignment: .leading)
                .frame(maxWidth: .infinity)
            }
            Divider()
            HStack {
                Spacer()
                Button(words.text(model.requiredMissing ? .notNow : .continue)) { model.dismiss() }
                    .buttonStyle(.borderedProminent)
                    #if !os(tvOS)
                    .keyboardShortcut(.defaultAction)
                    #endif
                    .accessibilityIdentifier("permissions.continue")
            }
            .padding(16)
        }
        .accessibilityIdentifier("permissions.walkthrough")
        .environment(\.layoutDirection, words.isRightToLeft ? .rightToLeft : .leftToRight)
        .environment(\.locale, words.locale)
        #if os(tvOS)
        .focusSection()
        #endif
    }
}

struct PermissionCard: View {
    let item: PermissionsModel.Item
    let busy: Bool
    let words: PermissionsStrings
    let appName: String
    let ask: () -> Void
    let openSettings: () -> Void

    private var id: String { "permissions.\(item.kind.rawValue)" }

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack(alignment: .firstTextBaseline) {
                Label(words.name(item.kind), systemImage: "network")
                    .font(.title3.bold())
                Text(words.text(item.isRequired ? .required : .optional))
                    .font(.caption.bold())
                    .padding(.horizontal, 8).padding(.vertical, 2)
                    .background(Capsule().fill(Color.brass.opacity(0.25)))
                Spacer()
                stateLabel
            }
            Text(words.text(.localNetworkWhy))
            Text(words.text(.localNetworkWithout)).foregroundStyle(.secondary)
            if let help {
                Text(help).font(.callout).accessibilityIdentifier("\(id).help")
            }
            HStack(spacing: 12) { actions }
        }
        .padding(16)
        .background(RoundedRectangle(cornerRadius: 12).fill(Color.mktSurface))
        .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.mktBorder))
    }

    @ViewBuilder private var stateLabel: some View {
        Group {
            if busy {
                HStack(spacing: 6) {
                    ProgressView().controlSize(.small)
                    Text(words.text(.checking))
                }
            } else {
                Label(words.state(item.state), systemImage: icon)
                    .foregroundStyle(tint)
            }
        }
        .font(.callout.bold())
        .accessibilityElement(children: .combine)
        .accessibilityIdentifier("\(id).state")
        .accessibilityValue(busy ? "checking" : item.state.rawValue)
    }

    private var icon: String {
        switch item.state {
        case .granted: "checkmark.circle.fill"
        case .denied: "xmark.octagon.fill"
        case .notAsked: "questionmark.circle"
        case .unknown: "exclamationmark.triangle"
        }
    }

    private var tint: Color {
        switch item.state {
        case .granted: .green
        case .denied: .red
        case .notAsked: .secondary
        case .unknown: .orange
        }
    }

    private var help: String? {
        switch item.state {
        case .granted: nil
        case .notAsked: words.text(.localNetworkAsk)
        case .denied: words.text(.localNetworkDeniedHow, appName, appName)
        case .unknown: words.text(.localNetworkUnknown)
        }
    }

    @ViewBuilder private var actions: some View {
        switch item.state {
        case .granted:
            EmptyView()
        case .notAsked:
            Button(words.text(.allow), action: ask)
                .buttonStyle(.borderedProminent)
                .disabled(busy)
                .accessibilityIdentifier("\(id).allow")
        case .denied:
            if item.kind.settingsURL != nil {
                Button(words.text(.openSettings), action: openSettings)
                    .buttonStyle(.borderedProminent)
                    .accessibilityIdentifier("\(id).openSettings")
            }
            Button(words.text(.checkAgain), action: ask)
                .disabled(busy)
                .accessibilityIdentifier("\(id).checkAgain")
        case .unknown:
            Button(words.text(.checkAgain), action: ask)
                .buttonStyle(.borderedProminent)
                .disabled(busy)
                .accessibilityIdentifier("\(id).checkAgain")
            if item.kind.settingsURL != nil {
                Button(words.text(.openSettings), action: openSettings)
                    .accessibilityIdentifier("\(id).openSettings")
            }
        }
    }
}
