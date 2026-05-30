#if os(tvOS)
import SwiftUI

public struct LibraryView: View {
    @EnvironmentObject var session: AppSession
    @State private var videos: [VideoSummary] = []

    public init() {}

    public var body: some View {
        ScrollView {
            LazyVGrid(columns: Array(repeating: GridItem(.fixed(320), spacing: 32), count: 4), spacing: 48) {
                ForEach(videos) { v in
                    Button(action: {}) {
                        PosterCard(item: v, showProgress: false)
                    }
                    .buttonStyle(.card)
                }
            }
            .padding(96)
        }
        .task {
            guard let cfg = session.apiConfig else { return }
            videos = (try? await LibraryService(gql: GraphQLClient(config: cfg)).listVideos()) ?? []
        }
    }
}

public struct SearchView: View {
    @EnvironmentObject var session: AppSession
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
                        Button(action: {}) {
                            PosterCard(item: v, showProgress: false)
                        }
                        .buttonStyle(.card)
                    }
                }
            }
        }
        .padding(96)
        .onChange(of: query) { _, new in
            Task {
                guard let cfg = session.apiConfig else { return }
                results = (try? await SearchService(gql: GraphQLClient(config: cfg)).query(new)) ?? []
            }
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
#endif
