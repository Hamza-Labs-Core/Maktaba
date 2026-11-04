import MaktabaKit
import SwiftUI

/// Search with the TV's keyboard (or the Siri remote's dictation): the home's videos
/// whose title or file matches, as you type, a moment after you stop.
struct SearchView: View {
    @Environment(\.client) private var client
    @State private var query = ""
    @State private var results: [Video] = []
    @State private var searched = ""
    /// The last search did not work (not "no results": the home did not answer, say).
    @State private var problem: HomeFailure?
    /// Asks again after a failure.
    @State private var attempt = 0

    private let columns = Array(repeating: GridItem(.fixed(VideoCard.width), spacing: 56), count: 4)

    @Environment(\.appSession) private var session

    var body: some View {
        ScrollView {
            // the TV searches titles by their words; while search by meaning is off on the
            // home an admin reads so under the field (51.11)
            if session?.capabilities.current.wordsOnlyLine == true {
                Text(verbatim: FeatureWords.wordsOnly)
                    .font(Theme.body(26))
                    .foregroundStyle(Theme.muted)
                    .frame(maxWidth: .infinity, alignment: .leading)
                    .padding(.horizontal, 80)
                    .accessibilityIdentifier("search.wordsOnly")
            }
            if let problem {
                ErrorBanner(failure: problem) { attempt += 1 }.padding(.horizontal, 80)
            } else if !searched.isEmpty && results.isEmpty {
                ContentUnavailableView.search(text: searched).padding(.top, 80)
            } else {
                LazyVGrid(columns: columns, spacing: 64) {
                    ForEach(results) { v in
                        NavigationLink {
                            VideoPage(id: v.id)
                        } label: {
                            VideoCard(id: v.id, title: v.displayTitle)
                        }
                        .buttonStyle(.borderless)
                    }
                }
                .padding(.horizontal, 80)
                .padding(.vertical, 48)
            }
        }
        .searchable(text: $query, prompt: "Search your libraries")
        .task(id: "\(attempt)|\(query)") {
            let q = query.trimmingCharacters(in: .whitespaces)
            guard !q.isEmpty else { results = []; searched = ""; problem = nil; return }
            // typing goes on: the cancelled wait ends this search
            do { try await Task.sleep(for: .milliseconds(400)) } catch { return }
            guard let client else { return }
            do {
                results = try await client.videos(library: nil, query: q).items
                searched = q
                problem = nil
            } catch {
                if let e = HomeFailure.shown("search", error) {
                    problem = e
                    results = []
                }
            }
        }
    }
}
