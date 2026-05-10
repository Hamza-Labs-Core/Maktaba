import SwiftUI

public struct LibraryView: View {
    @State private var videos: [VideoSummary] = []

    public init() {}

    public var body: some View {
        ScrollView {
            LazyVGrid(columns: Array(repeating: GridItem(.fixed(320), spacing: 32), count: 4), spacing: 48) {
                ForEach(videos) { v in
                    PosterCard(item: v, showProgress: false)
                }
            }
            .padding(96)
        }
        .task { videos = (try? await LibraryService().listVideos()) ?? [] }
    }
}

public struct SearchView: View {
    @State private var query: String = ""
    @State private var results: [VideoSummary] = []

    public init() {}

    public var body: some View {
        VStack(spacing: 32) {
            TextField("Search Maktaba", text: $query)
                .padding()
                .font(.title3)
            ScrollView {
                LazyVGrid(columns: Array(repeating: GridItem(.fixed(320), spacing: 32), count: 4)) {
                    ForEach(results) { v in
                        PosterCard(item: v, showProgress: false)
                    }
                }
            }
        }
        .padding(96)
        .onChange(of: query) { _, new in
            Task { results = (try? await SearchService().query(new)) ?? [] }
        }
    }
}

public struct SettingsView: View {
    public init() {}

    public var body: some View {
        Form {
            Section("Server") {
                Text("Connected")
            }
            Section("Account") {
                Button("Sign out") {}
            }
        }
    }
}
