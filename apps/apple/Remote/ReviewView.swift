import AVFoundation
import MaktabaKit
import SwiftUI

// Word review in the Remote (Epic 52, story 52.13): the web's queue on iPhone, iPad and Mac.
// A home admin opens it from Needs attention; the word plays its clip, shows the sentence in
// the transcript's direction with the word bold and underlined, and resolves with a tap or the
// web's keys on a hardware keyboard (J, K, Space, R, Return, 1-5, N, D, S, U). A 403 says only
// home admins review words; a 409 reloads the word. The entry hides when the summary cannot be
// read (not a home admin, a cloud-linked sign-in).

/// Needs attention's Word review row, with the open count; hidden when the home says no.
struct ReviewEntry: View {
    @Environment(RemoteModel.self) private var model
    @State private var open: Int?

    var body: some View {
        Group {
            if let open {
                Section {
                    NavigationLink {
                        ReviewView()
                    } label: {
                        Label("Word review", systemImage: "text.badge.checkmark")
                            .badge(open)
                    }
                    .accessibilityIdentifier("open-word-review")
                }
            }
        }
        .task { open = try? await model.client.reviewOpenCount() }
    }
}

struct ReviewView: View {
    @Environment(RemoteModel.self) private var model
    @Environment(\.layoutDirection) private var layout
    @State private var items: [ReviewFlag]?
    @State private var index = 0
    @State private var forbidden = false
    @State private var notice: LocalizedStringKey?
    @State private var lastResult: ReviewResult?
    @State private var toast: String?

    var body: some View {
        Group {
            if forbidden {
                Text("Only home admins can review words").accessibilityIdentifier("review-forbidden")
            } else if let items, items.isEmpty {
                Text("No words to review").foregroundStyle(.secondary)
            } else if let items {
                let current = items[min(index, items.count - 1)]
                ReviewWordView(flag: current, notice: notice,
                               move: { d in index = max(0, min(items.count - 1, index + d)) },
                               done: { label, r in
                                   self.items = items.filter { $0.id != current.id }
                                   index = min(index, max(0, items.count - 2))
                                   lastResult = r
                                   toast = label
                               },
                               conflict: { notice = "The sentence changed since this word was found: it is shown again as it is now."; Task { await load() } },
                               forbid: { forbidden = true })
                    .id(current.id)
            } else {
                ProgressView()
            }
        }
        .navigationTitle("Word review")
        .safeAreaInset(edge: .bottom) {
            if let toast {
                HStack {
                    Text(toast)
                    Spacer()
                    Button("Undo") { Task { await undo() } }
                        .keyboardShortcut("u", modifiers: [])
                }
                .padding()
                .background(.thinMaterial)
                .task(id: toast) {
                    try? await Task.sleep(for: .seconds(6))
                    if self.toast == toast { self.toast = nil }
                }
            }
        }
        .task { await load() }
        .refreshable { await load() }
    }

    private func load() async {
        do {
            items = try await model.client.reviewFlags().items.filter { $0.kind == "word" }
        } catch let e as APIError where e.isReviewForbidden {
            forbidden = true
        } catch {
            notice = "That did not work. Try again."
        }
    }

    private func undo() async {
        guard let r = lastResult else { return }
        lastResult = nil
        toast = nil
        try? await model.client.undoReview(r)
        await load()
    }
}

struct ReviewWordView: View {
    @Environment(RemoteModel.self) private var model
    let flag: ReviewFlag
    let notice: LocalizedStringKey?
    let move: (Int) -> Void
    let done: (String, ReviewResult) -> Void
    let conflict: () -> Void
    let forbid: () -> Void

    @State private var detail: ReviewFlag?
    @State private var edit = ""
    @State private var applyAll = false
    @State private var busy = false
    @State private var player: AVPlayer?
    @State private var noAudio = false
    @Environment(\.layoutDirection) private var ui

    private var f: ReviewFlag { detail ?? flag }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                if let notice { Text(notice).foregroundStyle(.secondary) }
                if !f.reviewersNote.isEmpty { Text(f.reviewersNote).font(.footnote) }
                Text(f.videoTitle).font(.caption).foregroundStyle(.secondary)
                sentence
                    .environment(\.layoutDirection, f.rightToLeft ? .rightToLeft : .leftToRight)
                HStack {
                    Button("Play / pause") { toggle(rate: 1) }.keyboardShortcut(.space, modifiers: [])
                    Button("Play at 0.75×") { toggle(rate: 0.75) }.keyboardShortcut(.space, modifiers: .shift)
                    Button("Replay") { replay() }.keyboardShortcut("r", modifiers: [])
                }
                .buttonStyle(.bordered)
                if noAudio { Text("Audio not available").foregroundStyle(.secondary) }
                ForEach(f.reasons, id: \.self) { r in Text(verbatim: "• ") + Text(LocalizedStringKey(reasonWords(r))) }
                if f.isQuran, let v = f.verse { verse(v) }
                candidates
                TextField("Spelling", text: $edit)
                    .textFieldStyle(.roundedBorder)
                actions
                if f.sameKeyOpen > 0 {
                    Toggle("Apply to all \(f.sameKeyOpen + 1) places", isOn: $applyAll)
                        .keyboardShortcut("a", modifiers: [])
                }
                HStack {
                    Button("Previous word") { move(-1) }.keyboardShortcut("k", modifiers: [])
                    Button("Next word") { move(1) }.keyboardShortcut("j", modifiers: [])
                }
                .buttonStyle(.borderless)
            }
            .padding()
        }
        // swipes mirror in right to left: towards the reading end is "next"
        .gesture(DragGesture(minimumDistance: 40).onEnded { g in
            let leftwards = g.translation.width < -80
            let rightwards = g.translation.width > 80
            if ui == .rightToLeft ? rightwards : leftwards { move(1) }
            if ui == .rightToLeft ? leftwards : rightwards { move(-1) }
        })
        .task {
            if let d = try? await model.client.reviewFlag(flag.id) { detail = d }
            edit = f.original
            if let url = try? await model.client.reviewClip(flag.id) {
                let p = AVPlayer(url: url)
                player = p
                p.play() // the clip plays as the word opens
            } else {
                noAudio = true
            }
        }
        .onDisappear { player?.pause() }
    }

    private var sentence: some View {
        let parts = f.parts
        var word = AttributedString(parts.word)
        word.inlinePresentationIntent = .stronglyEmphasized
        word.underlineStyle = .single
        return VStack(alignment: .leading, spacing: 6) {
            if !f.before.isEmpty { Text(f.before).font(.footnote).foregroundStyle(.secondary) }
            Text(AttributedString(parts.before) + word + AttributedString(parts.after))
                .font(.title3)
                .accessibilityIdentifier("review-sentence")
            if !f.after.isEmpty { Text(f.after).font(.footnote).foregroundStyle(.secondary) }
        }
    }

    @ViewBuilder private func verse(_ v: QuranRef) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            Text((ui == .rightToLeft ? v.labelAr : v.labelEn) ?? "")
            if let m = v.missing {
                Text("A word of the verse may be missing: \(m)")
            } else if v.kind == "ins" {
                Text("Not in the verse")
            } else if let e = v.expected {
                Text("The verse has: \(e)")
            }
            Text("Quran text: Tanzil Project (tanzil.net)").font(.footnote).foregroundStyle(.secondary)
        }
    }

    private var candidates: some View {
        VStack(alignment: .leading) {
            ForEach(Array(f.candidates.prefix(5).enumerated()), id: \.offset) { i, c in
                Button {
                    resolve(.correct, c.text)
                } label: {
                    Text(verbatim: "\(i + 1)  \(c.text)")
                }
                .buttonStyle(.bordered)
                .disabled(busy)
                .keyboardShortcut(KeyEquivalent(Character(String(i + 1))), modifiers: [])
            }
        }
    }

    private var actions: some View {
        HStack {
            Button("Accept as is") { resolve(.accept) }.keyboardShortcut(.return, modifiers: [])
            Button("Correct") { resolve(.correct, edit.trimmingCharacters(in: .whitespaces)) }
                .disabled(edit.trimmingCharacters(in: .whitespaces).isEmpty || edit == f.original)
            Button("Mark as a name") { resolve(.name) }.keyboardShortcut("n", modifiers: [])
            if !f.isQuran {
                Button("Add to dictionary") { resolve(.dictionary) }.keyboardShortcut("d", modifiers: [])
            }
            Button("Skip") { resolve(.skip) }.keyboardShortcut("s", modifiers: [])
        }
        .buttonStyle(.borderedProminent)
        .disabled(busy)
    }

    private func toggle(rate: Float) {
        guard let p = player else { return }
        if p.timeControlStatus == .playing { p.pause() } else { p.playImmediately(atRate: rate) }
    }

    private func replay() {
        player?.seek(to: .zero)
        player?.play()
    }

    private func resolve(_ action: ReviewAction, _ text: String? = nil) {
        guard !busy else { return }
        busy = true
        Task {
            defer { busy = false }
            do {
                let r = try await model.client.resolveFlag(f.id, action: action, text: text, applyToSameKey: applyAll)
                done(doneWords(action, text), r)
            } catch let e as APIError {
                if e.isReviewForbidden { forbid() } else if e.reviewConflict != nil { conflict() }
            } catch {}
        }
    }

    private func doneWords(_ a: ReviewAction, _ text: String?) -> String {
        switch a {
        case .correct: String(localized: "Fixed «\(f.original)» → «\(text ?? "")»")
        case .accept: String(localized: "Kept «\(f.original)» as it is")
        case .name: String(localized: "«\(f.original)» is a name now")
        case .dictionary: String(localized: "«\(f.original)» is in the dictionary now")
        case .skip: String(localized: "Skipped «\(f.original)»")
        }
    }
}

/// A reason in the web's words (review.reason.*).
func reasonWords(_ r: ReviewReason) -> String {
    switch r {
    case .lowConfidence: "Whisper was unsure"
    case .notInDictionary: "Not in the dictionary"
    case .listenersDisagree: "Heard differently"
    case .quranMismatch: "Differs from the Quran text"
    case .namedEntity: "Looks like a name"
    case .nearVocabulary: "Close to a known word"
    case .manual: "Picked by a reviewer"
    case .previousFix: "An earlier fix"
    case .overBudget: "Over the file's limit"
    case .unknown: "Another reason"
    }
}
