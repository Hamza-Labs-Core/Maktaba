import SwiftUI

/// A poster grid for a library's contents. Tapping (clicking the
/// Siri remote) a focused card pushes the player.
struct MediaGridView: View {
    let title: String
    let libraryID: String

    @Environment(\.api) private var api
    @State private var items: [Media] = []
    @State private var error: String?
    @State private var loading = true

    private let columns = Array(repeating: GridItem(.flexible(), spacing: 48), count: 5)

    var body: some View {
        ScrollView {
            if loading {
                ProgressView().padding(.top, 120)
            } else if let error {
                ErrorBanner(message: error) { Task { await load() } }
            } else {
                LazyVGrid(columns: columns, spacing: 56) {
                    ForEach(items) { media in
                        NavigationLink {
                            PlayerView(media: media)
                        } label: {
                            PosterCard(title: media.title, posterURL: media.posterURL)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .padding(48)
            }
        }
        .navigationTitle(title)
        .background(Theme.background.ignoresSafeArea())
        .task { await load() }
    }

    private func load() async {
        loading = true
        defer { loading = false }
        do {
            let resp: SearchResponse = try await api.get(
                "/api/libraries/\(libraryID)/items"
            )
            items = resp.items.map(\.asMedia)
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// The focusable poster primitive. The focus engine flips `isFocused`
/// as the user moves the D-pad; we respond with the theme's focus
/// treatment (`goldFocus`): a 3pt gold ring, a scale-up and a gold glow
/// so the focused card reads clearly from across the room.
struct PosterCard: View {
    let title: String
    var posterURL: String?
    /// Optional resume progress in `0...1`; draws a bar across the foot.
    var progress: Double = 0

    @FocusState private var isFocused: Bool

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            ZStack(alignment: .bottom) {
                poster
                if progress > 0 {
                    ProgressView(value: progress)
                        .tint(Theme.gold)
                        .padding(8)
                }
            }
            .frame(width: 300, height: 450)
            .clipShape(RoundedRectangle(cornerRadius: Theme.cornerRadius, style: .continuous))
            .goldFocus(isFocused, scale: 1.1)

            Text(title)
                .font(Theme.body(31, weight: .medium, relativeTo: .headline))
                .foregroundStyle(isFocused ? Theme.text : Theme.muted)
                .lineLimit(1)
                .frame(width: 300, alignment: .leading)
        }
        .focusable()
        .focused($isFocused)
    }

    @ViewBuilder
    private var poster: some View {
        if let urlString = posterURL, let url = URL(string: urlString) {
            AsyncImage(url: url) { image in
                image.resizable().aspectRatio(contentMode: .fill)
            } placeholder: {
                placeholder
            }
        } else {
            placeholder
        }
    }

    private var placeholder: some View {
        ZStack {
            Rectangle().fill(Theme.raised)
            Image(systemName: "film")
                .font(.system(size: 64))
                .foregroundStyle(Theme.faint)
        }
    }
}
