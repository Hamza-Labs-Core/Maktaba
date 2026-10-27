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
    @State private var problem: AppError?
    /// Asks again after a failure.
    @State private var attempt = 0

    private let columns = Array(repeating: GridItem(.fixed(VideoCard.width), spacing: 56), count: 4)

    var body: some View {
        ScrollView {
            if let problem {
                ErrorBanner(error: problem) { attempt += 1 }.padding(.horizontal, 80)
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
                if let e = AppError.shown("search", error) {
                    problem = e
                    results = []
                }
            }
        }
    }
}
