import MaktabaKit
import SwiftUI

/// The landing screen over the library scene: the home's rails (`GET
/// /api/recommendations`), continue watching first, since resuming is the first thing a
/// TV is for. A home with no rails yet shows its newest videos.
struct HomeView: View {
    @Environment(\.client) private var client
    @State private var rails: [Rail] = []
    @State private var newest: [Video] = []
    @State private var problem: AppError?
    @State private var loading = true

    var body: some View {
        ZStack {
            LibraryBackdrop()
            ScrollView {
                VStack(alignment: .leading, spacing: 40) {
                    SectionTitle("Your library", size: 52)
                        .readingPanel(padding: 28)
                        .padding(.top, 160)

                    Group {
                        if loading {
                            ProgressView().frame(maxWidth: .infinity).padding(.vertical, 60)
                        } else if let problem {
                            ErrorBanner(error: problem) { Task { await load() } }
                        } else if rails.isEmpty && newest.isEmpty {
                            ContentUnavailableView("Nothing to watch yet", systemImage: "books.vertical",
                                                   description: Text("Videos appear here once this home shares a library with you."))
                        } else if rails.isEmpty {
                            VideoRow(title: "Newly added", items: newest.map { RowItem(id: $0.id, title: $0.displayTitle) })
                        } else {
                            VStack(alignment: .leading, spacing: 48) {
                                ForEach(rails) { rail in
                                    VideoRow(title: Self.title(of: rail),
                                             items: rail.items.map { RowItem(id: $0.videoID, progress: $0.progress) })
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
        .ignoresSafeArea(edges: .top)
        .task { await load() }
        .refreshable { await load() }
    }

    /// A rail's title is the app's to say (the home sends a kind, not words).
    static func title(of rail: Rail) -> String {
        switch rail.reasonKind {
        case "continue_watching": "Continue watching"
        case "for_you": "For you"
        case "from_library": "From your libraries"
        case "newly_added": "Newly added"
        case "editor_picks": "Picks"
        default: "More to watch"
        }
    }

    private func load() async {
        guard let client else { return }
        do {
            rails = try await client.recommendations().filter { !$0.items.isEmpty }
            if rails.isEmpty {
                newest = try await client.videos(library: nil).items
            }
            problem = nil
        } catch {
            problem = AppError.shown("load home rails", error) ?? problem
        }
        loading = false
    }
}

struct RowItem: Identifiable, Hashable {
    let id: String
    var title: String?
    var progress: Double = 0
}

/// One labelled horizontal row of video cards.
struct VideoRow: View {
    let title: String
    let items: [RowItem]

    var body: some View {
        VStack(alignment: .leading, spacing: 20) {
            SectionTitle(title, size: 40)
            ScrollView(.horizontal) {
                LazyHStack(spacing: 48) {
                    ForEach(items) { item in
                        NavigationLink {
                            VideoPage(id: item.id)
                        } label: {
                            VideoCard(id: item.id, title: item.title, progress: item.progress)
                        }
                        .buttonStyle(.borderless)
                    }
                }
                .padding(.vertical, 32) // room for the focus scale to grow
            }
            .scrollClipDisabled()
        }
    }
}

/// A screen that could not load: MaktabaKit's error screen (what went wrong in the
/// person's words, Try again, Sign in again, Choose another home), in the TV's gold.
struct ErrorBanner: View {
    let error: AppError
    let retry: () -> Void

    var body: some View {
        ErrorStateView(error, retry: retry)
            .buttonStyle(.gold)
            .foregroundStyle(Theme.text)
            .frame(maxWidth: .infinity)
            .padding(.vertical, 60)
    }
}

extension AppError {
    /// What a TV screen shows for a failed load: nil when there is nothing to say (the
    /// screen moved on). Logged.
    static func shown(_ what: StaticString, _ error: Error) -> AppError? {
        let e = AppError(error)
        guard !e.isCancellation else { return nil }
        Log.failure(what, error)
        return e
    }
}
