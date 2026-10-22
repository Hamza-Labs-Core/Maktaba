import SwiftUI

/// The landing screen: a vertical stack of horizontally-scrolling
/// rails. "Continue Watching" comes first (resume is the #1 TV action),
/// followed by recommendation rails from `GET /api/recommendations`.
struct HomeView: View {
    @Environment(\.api) private var api
    @State private var rails: [MediaRail] = []
    @State private var error: String?
    @State private var loading = true

    var body: some View {
        ZStack {
            // The library scene sits behind everything; every piece of
            // text below is on a reading panel, never on the art itself.
            LibraryBackdrop()

            ScrollView {
                VStack(alignment: .leading, spacing: 40) {
                    // Headline in the calm middle of the scene, below the lamp.
                    VStack(alignment: .leading, spacing: 8) {
                        SectionTitle("Your library", size: 52)
                        Text("Continue where you left off")
                            .font(Theme.body(29))
                            .foregroundStyle(Theme.muted)
                    }
                    .readingPanel(padding: 28)
                    .frame(maxWidth: .infinity)
                    .padding(.top, 220)

                    Group {
                        if loading {
                            ProgressView()
                                .frame(maxWidth: .infinity)
                                .padding(.vertical, 60)
                        } else if let error {
                            ErrorBanner(message: error) { Task { await load() } }
                        } else {
                            VStack(alignment: .leading, spacing: 48) {
                                ForEach(rails) { rail in
                                    RailRow(rail: rail)
                                }
                            }
                        }
                    }
                    .readingPanel(padding: 40)
                }
                .padding(.vertical, 60)
                .padding(.horizontal, 80) // 10-foot safe-area gutter
            }
        }
        .task { await load() }
    }

    private func load() async {
        loading = true
        defer { loading = false }
        do {
            let recs: Recommendations = try await api.get("/api/recommendations")
            rails = recs.rails
            error = nil
        } catch {
            self.error = error.localizedDescription
        }
    }
}

/// One labelled horizontal rail of poster cards.
struct RailRow: View {
    let rail: MediaRail

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            SectionTitle(rail.title, size: 40)

            ScrollView(.horizontal, showsIndicators: false) {
                LazyHStack(spacing: 40) {
                    ForEach(rail.items) { item in
                        PosterCard(
                            title: rail.title,
                            posterURL: item.posterURL,
                            progress: item.progress
                        )
                    }
                }
                .padding(.vertical, 24) // room for the focus scale to grow
            }
        }
    }
}

/// Lightweight inline error state with a retry affordance.
struct ErrorBanner: View {
    let message: String
    let retry: () -> Void

    var body: some View {
        VStack(spacing: 24) {
            Image(systemName: "wifi.exclamationmark")
                .font(.system(size: 64))
                .foregroundStyle(Theme.goldText)
            Text(message)
                .font(Theme.body(38, relativeTo: .title3))
                .foregroundStyle(Theme.text)
                .multilineTextAlignment(.center)
            Button("Try Again", action: retry)
        }
        .frame(maxWidth: .infinity)
        .padding(.vertical, 60)
    }
}
