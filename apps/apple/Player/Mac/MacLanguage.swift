#if os(macOS)
import AppKit
import MaktabaKit
import SwiftUI

/// Profile's Language row on the Mac (story 13.16, the desktop design's D5): English, العربية
/// or the system's languages, chosen in the Player. A new choice asks first, in English and in
/// Arabic (`MacLanguageDialog`), then keeps it (`LanguageChoice.keep`), stops what plays so its
/// last position reaches the home, and starts the Player again; at that launch AppKit lays the
/// window out in the language's direction (`LanguageChoice.applyLayout`, `PlayerApp`): in
/// Arabic the sidebar and the traffic lights at the right. If the Player cannot start again,
/// the defaults go back to exactly what they were and a message says so, in both languages.
struct MacLanguageRow: View {
    /// The choice kept in the app's own defaults (none: the system's languages).
    @State private var kept = LanguageChoice.kept(in: MacLanguageRow.ownDefaults())
    /// The choice the person picked, while the dialog asks.
    @State private var asked: LanguageChoice?
    /// The Player could not start again.
    @State private var failed = false
    @State private var restarting = false

    var body: some View {
        Picker(selection: Binding { asked ?? kept } set: { new in
            if new.needsRelaunch(from: kept) { asked = new }
        }) {
            ForEach(LanguageChoice.allCases) { choice in
                Text(verbatim: choice.name).tag(choice)
            }
        } label: {
            Label {
                Text("Language", tableName: "Profile",
                     comment: "The app's language: on iPhone and iPad it opens the system's page for it; on the Mac it chooses it")
            } icon: {
                Image(systemName: "globe")
            }
        }
        .pickerStyle(.segmented)
        .disabled(restarting)
        .accessibilityIdentifier("profile.language")
        .sheet(item: $asked) { choice in
            let prompt = MacLanguagePrompt(choice)
            MacLanguageDialog(title: prompt.title, message: prompt.message,
                              primary: prompt.restart, cancel: prompt.cancel) {
                asked = nil
                Task { await restart(in: choice) }
            } onCancel: {
                asked = nil
            }
        }
        .sheet(isPresented: $failed) {
            let words = MacLanguagePrompt.failure
            MacLanguageDialog(title: words.title, message: words.message, primary: words.ok, cancel: nil) {
                failed = false
            } onCancel: {
                failed = false
            }
        }
    }

    /// Keeps `choice`, stops what plays (its last position sent), and starts the Player again;
    /// if it cannot, puts the defaults back as they were and says so.
    private func restart(in choice: LanguageChoice) async {
        restarting = true
        defer { restarting = false }
        let before = LanguageDefaults(domain: Self.ownDefaults())
        choice.keep(in: .standard)
        await Playback.stopAll()
        do {
            try await MacRelaunch.now()
        } catch {
            Log.failure("restart in the new language", error)
            before.restore(in: .standard)
            kept = LanguageChoice.kept(in: Self.ownDefaults())
            failed = true
        }
    }

    /// The app's own defaults (its persistent domain), without the system's.
    static func ownDefaults() -> [String: Any]? {
        UserDefaults.standard.persistentDomain(forName: Bundle.main.bundleIdentifier ?? "")
    }
}

/// One line of a dialog in both languages: its text and the language it is in, so it is drawn
/// in that language's direction and VoiceOver reads it in that language's voice.
struct BilingualLine: Equatable {
    let text: String
    let language: LanguageChoice

    /// The text marked with its language (the macOS SDK has no speech-language attribute of
    /// its own: VoiceOver takes the text's language, and the line's locale, `lineText`).
    var attributed: AttributedString {
        var a = AttributedString(text)
        a.languageIdentifier = language.rawValue
        return a
    }
}

/// The words of the dialog before the Player starts again in `choice`, in English and in
/// Arabic (the person may read only the language they are switching to): each line in both,
/// English first.
struct MacLanguagePrompt {
    let title: [BilingualLine]
    let message: [BilingualLine]
    let restart: [BilingualLine]
    let cancel: [BilingualLine]

    init(_ choice: LanguageChoice) {
        let question = switch choice {
        case .arabic: LocalizedStringResource("Start Maktaba Player again in Arabic?", table: "Mac",
                                              comment: "Asked before the Player restarts in Arabic, chosen in Profile")
        case .english: LocalizedStringResource("Start Maktaba Player again in English?", table: "Mac",
                                               comment: "Asked before the Player restarts in English, chosen in Profile")
        case .system: LocalizedStringResource("Start Maktaba Player again in the system's language?", table: "Mac",
                                              comment: "Asked before the Player restarts in the system's languages, chosen in Profile")
        }
        title = Self.both(question)
        message = Self.both(LocalizedStringResource("The Player closes and opens again in the new language. Anything playing stops.",
                                                    table: "Mac", comment: "Under the question before the Player restarts in another language"))
        restart = Self.both(LocalizedStringResource("Restart", table: "Mac",
                                                    comment: "Restarts the Player in the language chosen in Profile"))
        cancel = Self.both(LocalizedStringResource("Cancel", table: "Mac",
                                                   comment: "Keeps the Player in its language: no restart"))
    }

    /// The words when the Player could not start again.
    static var failure: (title: [BilingualLine], message: [BilingualLine], ok: [BilingualLine]) {
        (both(LocalizedStringResource("Maktaba Player could not start again", table: "Mac",
                                      comment: "The Player could not restart in the language chosen in Profile")),
         both(LocalizedStringResource("It stays in its language. Try again, or quit and open it.", table: "Mac",
                                      comment: "Under: the Player could not restart; its language is as it was")),
         both(LocalizedStringResource("OK", table: "Mac", comment: "Closes the message that the Player could not restart")))
    }

    /// `resource` in English, then in Arabic.
    static func both(_ resource: LocalizedStringResource) -> [BilingualLine] {
        [LanguageChoice.english, .arabic].map { language in
            var r = resource
            r.locale = language.locale
            return BilingualLine(text: String(localized: r), language: language)
        }
    }
}

extension LanguageChoice {
    /// The locale of the language's own words (a dialog that speaks both).
    var locale: Locale { Locale(identifier: language ?? "en") }
}

/// A small dialog in two languages: the title and the message line by line, each in its own
/// direction (the Arabic right to left) and voice; the primary button (Return) and Cancel
/// (Esc) say both. Used before the Player starts again in another language and when it could
/// not.
struct MacLanguageDialog: View {
    let title: [BilingualLine]
    let message: [BilingualLine]
    let primary: [BilingualLine]
    let cancel: [BilingualLine]?
    let onPrimary: () -> Void
    let onCancel: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
            HStack(alignment: .top, spacing: MaktabaTokens.Spacing.s3) {
                Image(systemName: "globe")
                    .font(.system(size: 22, weight: .medium))
                    .foregroundStyle(MaktabaTokens.Colors.accentFg.color)
                    .frame(width: 40, height: 40)
                    .background(Color.brass, in: .rect(cornerRadius: MaktabaTokens.Radius.md))
                    .accessibilityHidden(true)
                VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s1) {
                    ForEach(title, id: \.text) { line in
                        lineText(line).font(.maktabaBody(15, weight: .semibold, relativeTo: .headline))
                            .accessibilityAddTraits(.isHeader)
                    }
                }
            }
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                ForEach(message, id: \.text) { line in
                    lineText(line).font(.maktabaBody(13, relativeTo: .body)).foregroundStyle(Color.mktMuted)
                }
            }
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                Spacer(minLength: 0)
                if let cancel {
                    Button(role: .cancel, action: onCancel) { buttonText(cancel) }
                        .keyboardShortcut(.cancelAction)
                        .accessibilityIdentifier("profile.language.cancel")
                }
                Button(action: onPrimary) { buttonText(primary) }
                    .keyboardShortcut(.defaultAction)
                    .tint(.brass)
                    .accessibilityIdentifier("profile.language.restart")
            }
        }
        .padding(MaktabaTokens.Spacing.s6)
        .frame(width: 440)
    }

    /// A line in its own language's direction, at its start.
    private func lineText(_ line: BilingualLine) -> some View {
        Text(line.attributed)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .environment(\.layoutDirection, line.language == .arabic ? .rightToLeft : .leftToRight)
            .environment(\.locale, line.language.locale)
    }

    /// A button's words in both languages, each marked with its own.
    private func buttonText(_ lines: [BilingualLine]) -> Text {
        var joined = AttributedString()
        for (i, line) in lines.enumerated() {
            if i > 0 { joined += AttributedString(" · ") }
            joined += line.attributed
        }
        return Text(joined)
    }
}

/// Starts the Player again: a new instance of the app (the same bundle, its defaults now
/// holding the new language), then this one quits once it has started. Throws when the new
/// instance could not start; this one then goes on.
@MainActor enum MacRelaunch {
    static func now() async throws {
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.createsNewApplicationInstance = true
        _ = try await NSWorkspace.shared.openApplication(at: Bundle.main.bundleURL, configuration: configuration)
        NSApp.terminate(nil)
    }
}
#endif
