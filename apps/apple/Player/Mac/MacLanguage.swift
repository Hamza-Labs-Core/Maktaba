#if os(macOS)
import AppKit
import MaktabaKit
import SwiftUI

/// Profile's Language row on the Mac (story 13.16, the desktop design's D5): English or
/// العربية, chosen in the Player. A new language asks first, in English and in Arabic, then
/// keeps the choice (`LanguageChoice.keep`) and starts the Player again, so AppKit lays the
/// window out in the new direction: in Arabic the sidebar and the traffic lights at the right.
struct MacLanguageRow: View {
    private let current = LanguageChoice.current()
    /// The language the person picked, while the prompt asks.
    @State private var asked: LanguageChoice?

    var body: some View {
        Picker(selection: Binding { asked ?? current } set: { new in
            if new.needsRelaunch(from: current) { asked = new }
        }) {
            ForEach(LanguageChoice.allCases) { choice in
                Text(verbatim: choice.name).tag(choice)
            }
        } label: {
            Label {
                Text("Language", tableName: "Profile", comment: "The app's language; opens the system's page for it")
            } icon: {
                Image(systemName: "globe")
            }
        }
        .pickerStyle(.segmented)
        .accessibilityIdentifier("profile.language")
        .alert(Text(verbatim: asked.map { MacLanguagePrompt($0).title } ?? ""),
               isPresented: Binding { asked != nil } set: { if !$0 { asked = nil } }) {
            if let asked {
                let prompt = MacLanguagePrompt(asked)
                Button {
                    asked.keep(in: .standard)
                    MacRelaunch.now()
                } label: {
                    Text(verbatim: prompt.restart)
                }
                .keyboardShortcut(.defaultAction)
                .accessibilityIdentifier("profile.language.restart")
                Button(role: .cancel) {
                    self.asked = nil
                } label: {
                    Text(verbatim: prompt.cancel)
                }
            }
        } message: {
            Text(verbatim: asked.map { MacLanguagePrompt($0).message } ?? "")
        }
    }
}

/// The prompt before the Player starts again in `choice`, in English and in Arabic (the person
/// may read only the language they are switching to): each line in both, English first.
struct MacLanguagePrompt {
    let title: String
    let message: String
    let restart: String
    let cancel: String

    init(_ choice: LanguageChoice) {
        func both(_ resource: LocalizedStringResource, _ separator: String) -> String {
            LanguageChoice.allCases.map { language in
                var r = resource
                r.locale = language.locale
                return String(localized: r)
            }.joined(separator: separator)
        }
        title = both(choice == .arabic
            ? LocalizedStringResource("Start Maktaba Player again in Arabic?", table: "Mac",
                                      comment: "Asked before the Player restarts in Arabic, chosen in Profile")
            : LocalizedStringResource("Start Maktaba Player again in English?", table: "Mac",
                                      comment: "Asked before the Player restarts in English, chosen in Profile"), "\n")
        message = both(LocalizedStringResource("The Player closes and opens again in the new language. Anything playing stops.",
                                               table: "Mac", comment: "Under the question before the Player restarts in another language"), "\n\n")
        restart = both(LocalizedStringResource("Restart", table: "Mac",
                                               comment: "Restarts the Player in the language chosen in Profile"), " · ")
        cancel = both(LocalizedStringResource("Cancel", table: "Mac",
                                              comment: "Keeps the Player in its language: no restart"), " · ")
    }
}

/// Starts the Player again: a new instance of the app (the same bundle, its defaults now
/// holding the new language), then this one quits once it has started.
@MainActor enum MacRelaunch {
    static func now() {
        let configuration = NSWorkspace.OpenConfiguration()
        configuration.createsNewApplicationInstance = true
        NSWorkspace.shared.openApplication(at: Bundle.main.bundleURL, configuration: configuration) { _, error in
            Task { @MainActor in
                if let error {
                    // the choice is kept: the next launch is in the new language
                    Log.failure("restart in the new language", error)
                    return
                }
                NSApp.terminate(nil)
            }
        }
    }
}
#endif
