import SwiftUI

/// Grid of the household's libraries. Selecting one drills into its
/// `MediaGridView`. Wrapped in a `NavigationStack` so the tab owns its
/// own back-stack.
struct LibraryView: View {
    @Environment(\.api) private var api
    @State private var libraries: [Library] = []
    @State private var error: String?
    @State private var loading = true

    private let columns = Array(repeating: GridItem(.flexible(), spacing: 48), count: 4)

    var body: some View {
        NavigationStack {
            ScrollView {
                if loading {
                    ProgressView().padding(.top, 120)
                } else if let error {
                    ErrorBanner(message: error) { Task { await load() } }
                } else {
                    LazyVGrid(columns: columns, spacing: 56) {
                        ForEach(libraries) { library in
                            NavigationLink {
                                MediaGridView(title: library.name, libraryID: library.id)
                            } label: {
                                LibraryCard(library: library)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .padding(64)
                }
            }
            .navigationTitle("Libraries")
        }
        .task { await load() }
    }

    private func load() async {
        loading = true
        defer { loading = false }
        do {
            let resp: LibraryList = try await api.get("/api/libraries")
            libraries = resp.items
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// A focusable tile representing a whole library.
struct LibraryCard: View {
    let library: Library
    @FocusState private var isFocused: Bool

    var body: some View {
        VStack(spacing: 16) {
            Image(systemName: "books.vertical.fill")
                .font(.system(size: 72))
            Text(library.name)
                .font(.title3.weight(.semibold))
                .lineLimit(1)
            if let count = library.totalVideos {
                Text("\(count) items")
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }
        }
        .frame(width: 340, height: 240)
        .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
        .overlay(
            RoundedRectangle(cornerRadius: 20, style: .continuous)
                .stroke(.white, lineWidth: isFocused ? 6 : 0)
        )
        .scaleEffect(isFocused ? 1.08 : 1.0)
        .shadow(radius: isFocused ? 20 : 0)
        .animation(.easeOut(duration: 0.18), value: isFocused)
        .focusable()
        .focused($isFocused)
    }
}
