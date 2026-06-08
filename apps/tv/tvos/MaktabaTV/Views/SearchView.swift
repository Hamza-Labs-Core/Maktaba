import SwiftUI

/// Search screen. `.searchable` gives us the tvOS system keyboard and
/// the Siri-remote dictation (voice) button with no extra work — both
/// feed the same `query` binding. Results render in the shared poster
/// grid. Deeper voice integration (a Siri intent that opens the app
/// pre-searched) is a follow-up; see the README.
struct SearchView: View {
    @Environment(\.api) private var api
    @State private var query = ""
    @State private var results: [SearchResult] = []
    @State private var loading = false

    private let columns = Array(repeating: GridItem(.flexible(), spacing: 48), count: 5)

    var body: some View {
        NavigationStack {
            ScrollView {
                if loading {
                    ProgressView().padding(.top, 120)
                } else if results.isEmpty && !query.isEmpty {
                    ContentUnavailableView.search(text: query)
                        .padding(.top, 120)
                } else {
                    LazyVGrid(columns: columns, spacing: 56) {
                        ForEach(results) { result in
                            NavigationLink {
                                PlayerView(media: result.asMedia)
                            } label: {
                                PosterCard(title: result.title, posterURL: result.posterURL)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .padding(48)
                }
            }
            .navigationTitle("Search")
        }
        .searchable(text: $query, prompt: "Search libraries")
        // Debounced by SwiftUI's submit; running on every keystroke on a
        // remote keyboard would hammer the API, so we search on submit.
        .onSubmit(of: .search) { Task { await runSearch() } }
    }

    private func runSearch() async {
        let trimmed = query.trimmingCharacters(in: .whitespaces)
        guard !trimmed.isEmpty else { results = []; return }
        loading = true
        defer { loading = false }
        do {
            let resp: SearchResponse = try await api.get("/api/search", query: ["q": trimmed])
            results = resp.items
        } catch {
            results = []
        }
    }
}
