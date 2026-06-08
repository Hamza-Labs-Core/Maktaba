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
        ScrollView {
            VStack(alignment: .leading, spacing: 48) {
                if loading {
                    ProgressView().padding(.top, 120)
                } else if let error {
                    ErrorBanner(message: error) { Task { await load() } }
                } else {
                    ForEach(rails) { rail in
                        RailRow(rail: rail)
                    }
                }
            }
            .padding(.vertical, 60)
            .padding(.horizontal, 80) // 10-foot safe-area gutter
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
            Text(rail.title)
                .font(.title2.weight(.semibold))

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
            Text(message)
                .font(.title3)
                .multilineTextAlignment(.center)
            Button("Try Again", action: retry)
        }
        .frame(maxWidth: .infinity)
        .padding(.top, 120)
    }
}
