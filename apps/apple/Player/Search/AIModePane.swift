import MaktabaKit
import SwiftUI

/// Search's AI mode tab (story 49.14, Epic 49 §5.8) on the iPhone, the iPad and the Mac: the
/// home's language model answers a question from the passages of the person's libraries
/// (`POST /api/search/ai`, MaktabaKit's `AIModeModel`).
///
/// Each question shows its sources at once (each opens the player at its passage), then the
/// answer sentence by sentence, each with its citations, which open the player at the cited
/// moment. Under the last answer, a follow-up field asks in the same conversation; New
/// question starts again from the search field. While the question waits in line, its place
/// shows. A home without a model, with AI mode off, too busy, or older than AI mode says so in
/// the tab, not as an error.
struct AIModePane: View {
    let ai: AIModeModel
    /// What the search field holds (Ask “…” before the first question).
    let typed: String
    let home: URL
    let isAdmin: Bool
    /// Opens a route once the search field has let go (`SearchScreen.open`).
    let open: (PlayerRoute) -> Void
    /// Asks `q` as a new question (after the field lets go).
    let ask: (String) -> Void
    /// Asks `q` after the conversation (after the field lets go).
    let followUp: (String) -> Void

    @State private var next = ""
    @FocusState private var typingNext: Bool

    var body: some View {
        ScrollViewReader { proxy in
            List {
                if ai.turns.isEmpty {
                    intro
                }
                ForEach(Array(ai.turns.enumerated()), id: \.element.id) { i, turn in
                    TurnView(turn: turn, index: i, home: home, isAdmin: isAdmin, open: open,
                             again: { ai.askAgain() })
                        .id(turn.id)
                }
                if let last = ai.turns.last, !last.state.isRunning, Self.canFollowUp(last.state) {
                    followUpRow
                        .id("followUp")
                }
            }
            .listStyle(.plain)
            .accessibilityIdentifier("ai.pane")
            // the newest question in sight as it is asked
            .onChange(of: ai.turns.last?.id) { _, id in
                if let id { withAnimation { proxy.scrollTo(id, anchor: .top) } }
            }
        }
    }

    /// A conversation the home refused outright (no model, off, older) has nothing to follow.
    static func canFollowUp(_ s: AIModeModel.State) -> Bool {
        switch s {
        case .refused(.unavailable), .refused(.off), .refused(.missing): false
        default: true
        }
    }

    private var intro: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s4) {
            Label {
                Text(verbatim: AIWords.intro())
                    .font(.maktabaBody(15, relativeTo: .body))
                    .foregroundStyle(Color.mktMuted)
                    .fixedSize(horizontal: false, vertical: true)
            } icon: {
                Image(systemName: "sparkles").foregroundStyle(Color.mktBrassText)
            }
            .accessibilityIdentifier("ai.intro")
            let q = SearchModel.sendable(typed)
            if !q.isEmpty {
                Button {
                    ask(q)
                } label: {
                    Text(verbatim: AIWords.ask(q))
                }
                .buttonStyle(.borderless)
                .accessibilityIdentifier("ai.ask")
            }
        }
        .padding(.vertical, MaktabaTokens.Spacing.s4)
        .listRowBackground(Color.clear)
        .listRowSeparator(.hidden)
    }

    private var followUpRow: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                TextField(text: $next) { Text(verbatim: AIWords.followUpPrompt()) }
                    .textFieldStyle(.roundedBorder)
                    .font(.maktabaBody(17, relativeTo: .body))
                    .focused($typingNext)
                    .submitLabel(.send)
                    .onSubmit(sendNext)
                    .accessibilityIdentifier("ai.followUp")
                Button(action: sendNext) {
                    Image(systemName: "arrow.up.circle.fill")
                        .font(.maktabaBody(28, relativeTo: .title2))
                        .frame(minWidth: 44, minHeight: 44)
                }
                .buttonStyle(.borderless)
                .disabled(SearchModel.sendable(next).isEmpty)
                .accessibilityLabel(Text(verbatim: AIWords.send()))
                .accessibilityIdentifier("ai.followUp.send")
            }
            Button {
                next = ""
                ai.clear()
            } label: {
                Label { Text(verbatim: AIWords.newQuestion()) } icon: { Image(systemName: "square.and.pencil") }
            }
            .buttonStyle(.borderless)
            .accessibilityIdentifier("ai.new")
        }
        .padding(.vertical, MaktabaTokens.Spacing.s4)
        .listRowBackground(Color.clear)
        .listRowSeparator(.hidden)
    }

    private func sendNext() {
        let q = SearchModel.sendable(next)
        guard !q.isEmpty else { return }
        next = ""
        typingNext = false
        followUp(q)
    }
}

/// One question: its words, its place in line, its sources, its sentences and how it ended.
private struct TurnView: View {
    let turn: AIModeModel.Turn
    let index: Int
    let home: URL
    let isAdmin: Bool
    let open: (PlayerRoute) -> Void
    let again: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
            Text(verbatim: turn.question)
                .font(.maktabaBody(20, weight: .semibold, relativeTo: .title3))
                .foregroundStyle(Color.mktText)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityAddTraits(.isHeader)
                .accessibilityIdentifier("ai.question.\(index)")
            progress
            if !turn.passages.isEmpty {
                sources
            }
            if !turn.sentences.isEmpty {
                VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                    ForEach(Array(turn.sentences.enumerated()), id: \.offset) { i, s in
                        SentenceRow(turn: turn, sentence: s, id: "ai.sentence.\(index).\(i)", open: open)
                    }
                }
                .accessibilityElement(children: .contain)
                .accessibilityLabel(Text(verbatim: AIWords.answer()))
            }
            ending
        }
        .padding(.vertical, MaktabaTokens.Spacing.s3)
        .listRowBackground(Color.clear)
        .listRowSeparator(.hidden)
    }

    @ViewBuilder private var progress: some View {
        let line: String? = switch turn.state {
        case .asking: AIWords.asking()
        case let .waiting(position): AIWords.waiting(position)
        case .writing: AIWords.writing()
        default: nil
        }
        if let line {
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                ProgressView()
                Text(verbatim: line)
                    .font(.maktabaBody(15, relativeTo: .subheadline))
                    .foregroundStyle(Color.mktMuted)
                    .fixedSize(horizontal: false, vertical: true)
            }
            .accessibilityElement(children: .combine)
            .accessibilityIdentifier(turn.state == .writing ? "ai.writing.\(index)" : "ai.waiting.\(index)")
        }
    }

    /// The passages, each a card that plays from its moment: a row to scroll along.
    private var sources: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
            Text(verbatim: AIWords.sources())
                .font(.maktabaBody(15, weight: .semibold, relativeTo: .headline))
                .foregroundStyle(Color.mktBrassText)
                .accessibilityAddTraits(.isHeader)
            ScrollView(.horizontal) {
                LazyHStack(alignment: .top, spacing: MaktabaTokens.Spacing.s3) {
                    ForEach(turn.passages) { p in
                        Button {
                            open(.watch(videoID: p.videoID, at: p.startSec))
                        } label: {
                            SourceCard(passage: p, home: home)
                        }
                        .buttonStyle(.plain)
                        .accessibilityElement(children: .ignore)
                        // every word the card shows: the audit flags a shown word the label leaves out
                        .accessibilityLabel(Text(verbatim: AIWords.source(p)))
                        .accessibilityAddTraits(.isButton)
                        .accessibilityIdentifier("ai.source.\(index).\(p.n)")
                    }
                }
                .padding(.vertical, 2)
            }
            .scrollIndicators(.hidden)
        }
    }

    @ViewBuilder private var ending: some View {
        if let line = AIWords.ending(turn.state) {
            VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                Text(verbatim: line)
                    .font(.maktabaBody(15, relativeTo: .body))
                    .foregroundStyle(Color.mktText)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("ai.ending.\(index)")
                if case let .refused(.unavailable(settings, help)) = turn.state {
                    Text(verbatim: isAdmin ? AIWords.unavailableAdmin() : AIWords.unavailableViewer())
                        .font(.maktabaBody(15, relativeTo: .body))
                        .foregroundStyle(Color.mktMuted)
                        .fixedSize(horizontal: false, vertical: true)
                        .accessibilityIdentifier("ai.unavailable.\(isAdmin ? "admin" : "viewer")")
                    if isAdmin, let url = web(settings) {
                        Link(destination: url) { Text(verbatim: AIWords.openSettings()) }
                            .accessibilityIdentifier("ai.settings")
                    }
                    if let url = web(help ?? "/help/search#ai-mode") {
                        Link(destination: url) { Text(verbatim: AIWords.help()) }
                            .accessibilityIdentifier("ai.help")
                    }
                }
                if Self.canAskAgain(turn.state) {
                    Button(action: again) { Text(verbatim: AIWords.askAgain()) }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("ai.again.\(index)")
                }
            }
        }
    }

    static func canAskAgain(_ s: AIModeModel.State) -> Bool {
        switch s {
        case .stopped, .refused(.busy), .refused(.failed): true
        default: false
        }
    }

    /// A path of the home's web app (`/settings/rules?…`, `/help/search#ai-mode`) at this home.
    private func web(_ path: String?) -> URL? {
        guard let path, path.hasPrefix("/") else { return nil }
        return URL(string: path, relativeTo: home)?.absoluteURL
    }
}

/// A source: the video's poster, its title, the moment and the passage's words.
private struct SourceCard: View {
    let passage: AIPassage
    let home: URL
    @ScaledMetric(relativeTo: .body) private var width: CGFloat = 220

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s1) {
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                Text(verbatim: AIWords.mark(passage.n))
                    .font(.maktabaBody(13, weight: .semibold, relativeTo: .caption))
                    .foregroundStyle(Color.mktBrassText)
                    .padding(.horizontal, 6)
                    .background(Color.mktRaised, in: Capsule())
                Label { Text(verbatim: AIWords.clock(passage.startSec)) } icon: { Image(systemName: "play.fill") }
                    .font(.maktabaBody(13, weight: .medium, relativeTo: .caption))
                    .foregroundStyle(Color.mktBrassText)
                    // a time reads left to right in Arabic too
                    .environment(\.layoutDirection, .leftToRight)
            }
            Text(verbatim: passage.title)
                .font(.maktabaBody(15, weight: .medium, relativeTo: .subheadline))
                .foregroundStyle(Color.mktText)
                .fixedSize(horizontal: false, vertical: true)
            Text(verbatim: passage.text)
                .font(.maktabaBody(13, relativeTo: .footnote))
                .foregroundStyle(Color.mktMuted)
                .fixedSize(horizontal: false, vertical: true)
            if let l = passage.libraryName {
                Text(verbatim: l)
                    .font(.maktabaBody(12, relativeTo: .caption2))
                    .foregroundStyle(Color.mktMuted)
                    .fixedSize(horizontal: false, vertical: true)
            }
        }
        .frame(width: min(width, 340), alignment: .topLeading)
        .padding(MaktabaTokens.Spacing.s3)
        .overlay(RoundedRectangle(cornerRadius: MaktabaTokens.Radius.sm).stroke(Color.mktBorder))
        .contentShape(Rectangle())
    }
}

/// A sentence of the answer, then a button per citation (its passage's number), each opening
/// the player at the cited moment.
private struct SentenceRow: View {
    let turn: AIModeModel.Turn
    let sentence: AISentence
    let id: String
    let open: (PlayerRoute) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s1) {
            Text(verbatim: sentence.text)
                .font(.maktabaBody(17, relativeTo: .body))
                .foregroundStyle(Color.mktText)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityIdentifier(id)
            HStack(spacing: MaktabaTokens.Spacing.s2) {
                ForEach(Array(sentence.citations.enumerated()), id: \.offset) { _, c in
                    if let target = turn.target(c) {
                        let title = turn.passage(c.n)?.title ?? ""
                        Button {
                            open(.watch(videoID: target.videoID, at: target.startSec))
                        } label: {
                            HStack(spacing: 4) {
                                Image(systemName: "play.fill").font(.maktabaBody(10, relativeTo: .caption2))
                                Text(verbatim: AIWords.mark(c.n))
                            }
                            .font(.maktabaBody(13, weight: .semibold, relativeTo: .caption))
                            .foregroundStyle(Color.mktBrassText)
                            .padding(.horizontal, MaktabaTokens.Spacing.s2)
                            .frame(minWidth: 44, minHeight: 44)
                            .background(Color.mktRaised, in: Capsule())
                            .contentShape(Capsule())
                        }
                        .buttonStyle(.plain)
                        .accessibilityLabel(Text(verbatim: AIWords.citation(c.n, title: title, at: AIWords.clock(target.startSec))))
                        .accessibilityIdentifier("\(id).cite.\(c.n)")
                    }
                }
            }
        }
    }
}
