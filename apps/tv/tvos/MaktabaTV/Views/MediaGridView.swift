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
/// as the user moves the D-pad; we respond with the tvOS-standard
/// "lift": a scale-up, a brighter ring, and a drop shadow so the
/// focused card reads clearly from across the room.
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
                        .tint(.white)
                        .padding(8)
                }
            }
            .frame(width: 300, height: 450)
            .clipShape(RoundedRectangle(cornerRadius: 16, style: .continuous))
            .overlay(
                RoundedRectangle(cornerRadius: 16, style: .continuous)
                    .stroke(.white, lineWidth: isFocused ? 6 : 0)
            )
            .shadow(radius: isFocused ? 24 : 0)
            .scaleEffect(isFocused ? 1.12 : 1.0)
            .animation(.easeOut(duration: 0.18), value: isFocused)

            Text(title)
                .font(.headline)
                .lineLimit(1)
                .frame(width: 300, alignment: .leading)
                .opacity(isFocused ? 1 : 0.7)
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
            Rectangle().fill(.gray.opacity(0.3))
            Image(systemName: "film")
                .font(.system(size: 64))
                .foregroundStyle(.secondary)
        }
    }
}
