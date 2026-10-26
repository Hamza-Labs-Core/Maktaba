import MaktabaKit
import SwiftUI

/// Search with the TV's keyboard (or the Siri remote's dictation): the home's videos
/// whose title or file matches, as you type, a moment after you stop.
struct SearchView: View {
    @Environment(\.client) private var client
    @State private var query = ""
    @State private var results: [Video] = []
    @State private var searched = ""

    private let columns = Array(repeating: GridItem(.fixed(VideoCard.width), spacing: 56), count: 4)

    var body: some View {
        ScrollView {
            if !searched.isEmpty && results.isEmpty {
                ContentUnavailableView.search(text: searched).padding(.top, 80)
            } else {
                LazyVGrid(columns: columns, spacing: 64) {
                    ForEach(results) { v in
                        NavigationLink(value: VideoRoute(id: v.id)) {
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
        .task(id: query) {
            let q = query.trimmingCharacters(in: .whitespaces)
            guard !q.isEmpty else { results = []; searched = ""; return }
            try? await Task.sleep(for: .milliseconds(400))
            guard !Task.isCancelled, let client else { return }
            results = (try? await client.videos(library: nil, query: q).items) ?? []
            searched = q
        }
    }
}
