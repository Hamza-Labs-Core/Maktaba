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
            .background(Theme.background.ignoresSafeArea())
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
                .foregroundStyle(Theme.goldText)
            Text(library.name)
                .font(Theme.heading(40, relativeTo: .title3))
                .foregroundStyle(Theme.text)
                .lineLimit(1)
            if let count = library.totalVideos {
                Text("\(count) items")
                    .font(Theme.body(29, relativeTo: .callout))
                    .foregroundStyle(Theme.muted)
            }
        }
        .frame(width: 340, height: 240)
        .background(
            isFocused ? Theme.raised : Theme.surface,
            in: RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
        )
        .overlay(
            RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous)
                .strokeBorder(Theme.border, lineWidth: 1)
        )
        .goldFocus(isFocused)
        .focusable()
        .focused($isFocused)
    }
}
