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
        URL(string: "x-apple.systempreferences:com.apple.preference.security?\(self == .camera ? "Privacy_Camera" : "Privacy_LocalNetwork")")
        #else
        nil
        #endif
    }
}

public extension View {
    /// Shows the permissions walkthrough over the app: on the first run, and again whenever
    /// a required permission is refused; re-checked each time the app comes to the
    /// foreground. `appName` is the name the person sees in Settings (the bundle's display
    /// name by default).
    func permissionsWalkthrough(appName: String = Bundle.main.displayName) -> some View {
        modifier(PermissionsGate(appName: appName))
    }
}

extension Bundle {
    /// The name under the icon and in Settings.
    public var displayName: String {
        (object(forInfoDictionaryKey: "CFBundleDisplayName") as? String)
            ?? (object(forInfoDictionaryKey: "CFBundleName") as? String) ?? "Maktaba"
    }
}

struct PermissionsGate: ViewModifier {
    let appName: String
    // evaluated again each time the parent's body runs (only the first is kept), so it must
    // have no side effect: the launch reset is `PermissionsModel.applyLaunchArguments`, at start
    @State private var model = PermissionsModel.forThisApp()
    @Environment(\.scenePhase) private var phase

    func body(content: Content) -> some View {
        presented(content)
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

    #if os(iOS)
    /// On iPhone and iPad the walkthrough is drawn over the app in its own view, not in a
    /// UIKit sheet: UIKit dropped the sheet's dismissal, both the binding going false and the
    /// sheet's own dismiss, with Continue tapped well after the sheet stood still (iPad runs of
    /// PRs #595 and #608), so the walkthrough stayed. Here Continue's state change closes it
    /// the moment it is made, with no presentation to finish first.
    private func presented(_ content: Content) -> some View {
        content
            // the app under it is out of reach for VoiceOver (and the UI tests) while it is up
            .accessibilityHidden(model.isPresented)
            .overlay {
                if model.isPresented {
                    PermissionsCover(model: model, appName: appName)
                        .transition(.opacity)
                }
            }
            .animation(.easeInOut(duration: 0.2), value: model.isPresented)
    }
    #else
    private func presented(_ content: Content) -> some View {
        content
            .sheet(isPresented: $model.isPresented, onDismiss: { model.dismiss() }) {
                PermissionsWalkthrough(model: model, appName: appName)
                    #if os(macOS)
                    .frame(minWidth: 520, minHeight: 440)
                    #endif
            }
    }
    #endif
}

#if os(iOS)
/// The walkthrough over the app: the whole screen on an iPhone, a card over a dimmed app on
/// an iPad (where a sheet stood). The dimmed part takes the touches, so the app under it
/// gets none until Continue.
struct PermissionsCover: View {
    let model: PermissionsModel
    let appName: String
    @Environment(\.horizontalSizeClass) private var width

    var body: some View {
        if width == .regular {
            ZStack {
                Color.black.opacity(0.4)
                    .ignoresSafeArea()
                    .accessibilityHidden(true)
                PermissionsWalkthrough(model: model, appName: appName)
                    .frame(maxWidth: 580, maxHeight: 680)
                    .background(.background, in: RoundedRectangle(cornerRadius: 20))
                    .overlay(RoundedRectangle(cornerRadius: 20).stroke(.separator))
                    .clipShape(RoundedRectangle(cornerRadius: 20))
                    .padding(24)
            }
            .accessibilityAddTraits(.isModal)
        } else {
            PermissionsWalkthrough(model: model, appName: appName)
                .background(.background)
                .accessibilityAddTraits(.isModal)
        }
    }
}
#endif

/// Each permission the app needs: why, where it stands, and a button that asks or opens the
/// settings pane. English or Arabic (right to left) after the person's first language.
public struct PermissionsWalkthrough: View {
    let model: PermissionsModel
    let appName: String
    let words: PermissionsStrings
    @Environment(\.openURL) private var openURL
    /// Closes the sheet this walkthrough is in (Mac, Apple TV), whatever its binding says.
    @Environment(\.dismiss) private var close

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
                // the model's flag closes it; in a sheet (Mac, Apple TV) the sheet's own
                // dismiss too
                Button(words.text(model.requiredMissing ? .notNow : .continue)) {
                    model.dismiss()
                    #if !os(iOS)
                    close()
                    #endif
                }
                    .buttonStyle(.borderedProminent)
                    #if !os(tvOS)
                    .keyboardShortcut(.defaultAction)
                    #endif
                    .accessibilityIdentifier("permissions.continue")
            }
            .padding(16)
        }
        // its own element holding the others, so the buttons keep their identifiers
        .accessibilityElement(children: .contain)
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
                Label(words.name(item.kind), systemImage: PermissionsStrings.symbol(item.kind))
                    .font(.title3.bold())
                Text(words.text(item.isRequired ? .required : .optional))
                    .font(.caption.bold())
                    .padding(.horizontal, 8).padding(.vertical, 2)
                    .background(Capsule().fill(Color.brass.opacity(0.25)))
            }
            stateLabel
            Text(words.why(item.kind))
            Text(words.without(item.kind)).foregroundStyle(.secondary)
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
        case .notAsked: words.ask(item.kind)
        case .denied: words.deniedHow(item.kind, appName: appName)
        case .unknown: words.unknownHelp(item.kind)
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
