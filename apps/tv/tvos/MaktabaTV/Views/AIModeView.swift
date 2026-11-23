import MaktabaKit
import SwiftUI

/// Search's AI mode tab on Apple TV (story 49.14), driven by the Siri remote: the Player's tab
/// (`apps/apple/Player/Search/AIModePane.swift`) with the same model, MaktabaKit's `AIModeModel`.
///
/// Each question shows its place in line while it waits, then its sources (a row of cards, each
/// playing from its passage) and the answer sentence by sentence, each sentence followed by its
/// citations, which play the video from the cited moment. The question is typed in the search
/// field and asked from **Ask “…”** under the keyboard (`SearchView`); **Ask as a follow-up**
/// asks it in the same conversation. Rows are focus sections, so the focus moves between them
/// with Up and Down and along them with Left and Right.
struct TVAIModePane: View {
    let ai: AIModeModel
    let client: APIClient
    let isAdmin: Bool
    @Environment(\.appSession) private var session
    /// The video playing from a citation.
    @State private var playing: TVPlayback?
    @State private var played: TVPlayback?

    var body: some View {
        VStack(alignment: .leading, spacing: 56) {
            if ai.turns.isEmpty {
                Label {
                    Text(verbatim: AIWords.intro())
                } icon: {
                    Image(systemName: "sparkles").foregroundStyle(Theme.goldText)
                }
                .font(Theme.body(30))
                .foregroundStyle(Theme.muted)
                .accessibilityIdentifier("ai.intro")
            }
            ForEach(Array(ai.turns.enumerated()), id: \.element.id) { i, turn in
                TVTurn(turn: turn, index: i, home: client.home, isAdmin: isAdmin, play: play, again: { ai.askAgain() })
            }
            if let last = ai.turns.last, !last.state.isRunning, TVTurn.canFollowUp(last.state) {
                Button {
                    ai.clear()
                } label: {
                    Label { Text(verbatim: AIWords.newQuestion()) } icon: { Image(systemName: "square.and.pencil") }
                        .font(Theme.body(28, weight: .medium))
                }
                .accessibilityIdentifier("ai.new")
                .focusSection()
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .fullScreenCover(item: $playing, onDismiss: stopped) { PlayerScreen(playback: $0) }
    }

    /// Plays `videoID` from `at` seconds.
    private func play(_ videoID: String, _ at: Double) {
        Task {
            do {
                let v = try await client.video(videoID)
                let p = TVPlayback(client: client, video: v)
                if let parental = session?.parental { p.limited = { parental.current.limited } }
                p.start(at: at)
                played = p
                playing = p
            } catch {
                if case APIError.signedOut = error { session?.signedOut(); return }
                Log.failure("ai mode play", error)
            }
        }
    }

    private func stopped() {
        let p = played
        played = nil
        Task { await p?.stop() }
    }
}

/// One question and its answer.
private struct TVTurn: View {
    let turn: AIModeModel.Turn
    let index: Int
    let home: URL
    let isAdmin: Bool
    let play: (String, Double) -> Void
    let again: () -> Void

    static func canFollowUp(_ s: AIModeModel.State) -> Bool {
        switch s {
        case .refused(.unavailable), .refused(.off), .refused(.missing): false
        default: true
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 28) {
            Text(verbatim: turn.question)
                .font(Theme.heading(44))
                .foregroundStyle(Theme.text)
                .accessibilityAddTraits(.isHeader)
                .accessibilityIdentifier("ai.question.\(index)")
            progress
            if !turn.passages.isEmpty {
                sources
            }
            ForEach(Array(turn.sentences.enumerated()), id: \.offset) { i, s in
                sentence(s, id: "ai.sentence.\(index).\(i)")
            }
            ending
        }
    }

    @ViewBuilder private var progress: some View {
        let line: String? = switch turn.state {
        case .asking: AIWords.asking()
        case let .waiting(position): AIWords.waiting(position)
        case .writing: AIWords.writing()
        default: nil
        }
        if let line {
            HStack(spacing: 20) {
                ProgressView()
                Text(verbatim: line).font(Theme.body(28)).foregroundStyle(Theme.muted)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier(turn.state == .writing ? "ai.writing.\(index)" : "ai.waiting.\(index)")
        }
    }

    private var sources: some View {
        VStack(alignment: .leading, spacing: 16) {
            Text(verbatim: AIWords.sources())
                .font(Theme.heading(32))
                .foregroundStyle(Theme.goldText)
                .accessibilityAddTraits(.isHeader)
            ScrollView(.horizontal) {
                HStack(alignment: .top, spacing: 40) {
                    ForEach(turn.passages) { p in
                        Button {
                            play(p.videoID, p.startSec)
                        } label: {
                            VStack(alignment: .leading, spacing: 10) {
                                HStack(spacing: 12) {
                                    Text(verbatim: AIWords.mark(p.n)).foregroundStyle(Theme.goldText)
                                    Label { Text(verbatim: AIWords.clock(p.startSec)) } icon: { Image(systemName: "play.fill") }
                                        .environment(\.layoutDirection, .leftToRight)
                                }
                                .font(Theme.body(24, weight: .semibold))
                                Text(verbatim: p.title).font(Theme.body(26, weight: .medium)).lineLimit(2)
                                Text(verbatim: p.text).font(Theme.body(22)).foregroundStyle(.secondary).lineLimit(3)
                            }
                            .frame(width: 460, alignment: .topLeading)
                            .padding(24)
                        }
                        .buttonStyle(.card)
                        .accessibilityElement(children: .ignore)
                        .accessibilityLabel(Text(verbatim: AIWords.source(p)))
                        .accessibilityAddTraits(.isButton)
                        .accessibilityIdentifier("ai.source.\(index).\(p.n)")
                    }
                }
                .padding(.vertical, 24)
            }
            .scrollClipDisabled()
        }
        .focusSection()
    }

    /// A sentence, then its citations: each plays the video from the cited moment.
    private func sentence(_ s: AISentence, id: String) -> some View {
        VStack(alignment: .leading, spacing: 12) {
            Text(verbatim: s.text)
                .font(Theme.body(32))
                .foregroundStyle(Theme.text)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier(id)
            HStack(spacing: 24) {
                ForEach(Array(s.citations.enumerated()), id: \.offset) { _, c in
                    if let target = turn.target(c) {
                        let title = turn.passage(c.n)?.title ?? ""
                        Button {
                            play(target.videoID, target.startSec)
                        } label: {
                            Label {
                                Text(verbatim: "\(AIWords.mark(c.n))  ·  \(AIWords.clock(target.startSec))")
                            } icon: {
                                Image(systemName: "play.fill")
                            }
                            .font(Theme.body(22, weight: .semibold))
                        }
                        .accessibilityLabel(Text(verbatim: AIWords.citation(c.n, title: title, at: AIWords.clock(target.startSec))))
                        .accessibilityIdentifier("\(id).cite.\(c.n)")
                    }
                }
            }
        }
        .focusSection()
    }

    @ViewBuilder private var ending: some View {
        if let line = AIWords.ending(turn.state) {
            VStack(alignment: .leading, spacing: 20) {
                Text(verbatim: line)
                    .font(Theme.body(30))
                    .foregroundStyle(Theme.text)
                    .accessibilityIdentifier("ai.ending.\(index)")
                if case let .refused(.unavailable(_, help)) = turn.state {
                    // no browser on a TV: where an admin goes, in words, and the help's address
                    Text(verbatim: isAdmin ? AIWords.unavailableAdmin() : AIWords.unavailableViewer())
                        .font(Theme.body(26))
                        .foregroundStyle(Theme.muted)
                        .accessibilityIdentifier("ai.unavailable.\(isAdmin ? "admin" : "viewer")")
                    if let url = URL(string: help ?? "/help/search#ai-mode", relativeTo: home)?.absoluteURL {
                        Text(verbatim: AIWords.helpAt(url.absoluteString))
                            .font(Theme.body(24))
                            .foregroundStyle(Theme.muted)
                            .accessibilityIdentifier("ai.help")
                    }
                }
                if Self.canAskAgain(turn.state) {
                    Button(action: again) {
                        Text(verbatim: AIWords.askAgain()).font(Theme.body(26, weight: .medium))
                    }
                    .accessibilityIdentifier("ai.again.\(index)")
                }
            }
            .focusSection()
        }
    }

    static func canAskAgain(_ s: AIModeModel.State) -> Bool {
        switch s {
        case .stopped, .refused(.busy), .refused(.failed): true
        default: false
        }
    }
}
