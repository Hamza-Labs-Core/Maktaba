import MaktabaKit
import SwiftUI

/// The home's libraries (`GET /api/libraries/summary`: the pinned first, each with its newest
/// videos for its picture, as the iPhone/iPad Player's Libraries); one opens its videos.
struct LibraryView: View {
    @Environment(\.client) private var client
    @State private var libraries: [LibrarySummary] = []
    @State private var problem: HomeFailure?
    @State private var loading = true

    private let columns = Array(repeating: GridItem(.flexible(), spacing: 48), count: 4)

    var body: some View {
        ScrollView {
            if loading {
                ProgressView().padding(.top, 120)
            } else if let problem {
                ErrorBanner(failure: problem) { Task { await load() } }
            } else if libraries.isEmpty {
                ContentUnavailableView("No libraries", systemImage: "books.vertical",
                                       description: Text("This home shares no library with you yet."))
            } else {
                LazyVGrid(columns: columns, spacing: 56) {
                    ForEach(libraries) { library in
                        NavigationLink {
                            VideoGrid(title: library.name, library: library.id)
                        } label: {
                            LibraryCard(library: library)
                        }
                        .buttonStyle(.borderless)
                    }
                }
                .padding(.horizontal, 80)
                .padding(.vertical, 48)
            }
        }
        .background(Theme.background.ignoresSafeArea())
        .task { await load() }
    }

    private func load() async {
        guard let client else { return }
        do {
            libraries = LibrarySummary.ordered(try await client.librarySummaries())
            problem = nil
        } catch {
            problem = HomeFailure.shown("load libraries", error) ?? problem
        }
        loading = false
    }
}

/// A library's tile: its newest video's picture (the books while it loads, or when the
/// library has none yet), its name and how many videos it has.
struct LibraryCard: View {
    static let width: CGFloat = 380
    let library: LibrarySummary
    @Environment(\.client) private var client
    @Environment(\.isFocused) private var focused
    @State private var art = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            Group {
                if let client {
                    AuthorizedImage(client: client, video: library.posterVideoIDs.first, small: true,
                                    placeholder: "books.vertical.fill") { art = $0 }
                } else {
                    Rectangle().fill(Theme.surface)
                }
            }
            .frame(width: Self.width, height: Self.width * 9 / 16)
            .clipShape(.rect(cornerRadius: Theme.cornerRadius))
            .overlay(RoundedRectangle(cornerRadius: Theme.cornerRadius).strokeBorder(Theme.border, lineWidth: 1))
            .goldFocus(focused)
            Text(library.name)
                .font(Theme.heading(36))
                .foregroundStyle(focused ? Theme.text : Theme.muted)
                .lineLimit(1)
                .frame(width: Self.width, alignment: .leading)
        }
        // the UI tests read whether the picture showed
        .accessibilityIdentifier(art ? "library.card.\(library.id).art" : "library.card.\(library.id)")
    }
}

/// A library's videos (or every video), newest first, a page at a time.
struct VideoGrid: View {
    let title: String
    let library: String?

    @Environment(\.client) private var client
    @State private var videos: [Video] = []
    @State private var next: String?
    @State private var problem: HomeFailure?
    @State private var loading = true

    private let columns = Array(repeating: GridItem(.fixed(VideoCard.width), spacing: 56), count: 4)

    var body: some View {
        ScrollView {
            if loading {
                ProgressView().padding(.top, 120)
            } else if let problem {
                ErrorBanner(failure: problem) { Task { await load(more: false) } }
            } else if videos.isEmpty {
                ContentUnavailableView("No videos yet", systemImage: "film")
            } else {
                LazyVGrid(columns: columns, spacing: 64) {
                    ForEach(videos) { v in
                        NavigationLink {
                            VideoPage(id: v.id)
                        } label: {
                            VideoCard(id: v.id, title: v.displayTitle)
                        }
                        .buttonStyle(.borderless)
                        .onAppear {
                            if v.id == videos.last?.id, next != nil { Task { await load(more: true) } }
                        }
                    }
                }
                .padding(.horizontal, 80)
                .padding(.vertical, 48)
            }
        }
        .navigationTitle(title)
        .background(Theme.background.ignoresSafeArea())
        .task { await load(more: false) }
    }

    private func load(more: Bool) async {
        guard let client else { return }
        do {
            let page = try await client.videos(library: library, cursor: more ? next : nil)
            videos = more ? videos + page.items.filter { v in !videos.contains { $0.id == v.id } } : page.items
            next = page.next
            problem = nil
        } catch {
            let f = HomeFailure.shown("load videos", error)
            if !more { problem = f ?? problem }
        }
        loading = false
    }
}

/// A video's card: its picture (16:9, loaded with the token), how much was watched, and
/// its title under it. Focus lifts the picture with the gold ring.
struct VideoCard: View {
    static let width: CGFloat = 400
    let id: String
    var title: String?
    var progress: Double = 0

    @Environment(\.client) private var client
    @Environment(\.isFocused) private var focused
    @State private var loaded: String?
    @State private var art = false

    var body: some View {
        VStack(alignment: .leading, spacing: 16) {
            ZStack(alignment: .bottom) {
                if let client {
                    AuthorizedImage(client: client, video: id, small: true) { art = $0 }
                }
                if progress > 0 {
                    ProgressView(value: progress)
                        .tint(Theme.gold)
                        .padding(12)
                }
            }
            .frame(width: Self.width, height: Self.width * 9 / 16)
            .clipShape(.rect(cornerRadius: Theme.cornerRadius))
            .goldFocus(focused, scale: 1.1)

            Text(title ?? loaded ?? " ")
                .font(Theme.body(28, weight: .medium))
                .foregroundStyle(focused ? Theme.text : Theme.muted)
                .lineLimit(2, reservesSpace: true)
                .frame(width: Self.width, alignment: .leading)
        }
        // the UI tests read whether the picture showed
        .accessibilityIdentifier(art ? "video.card.\(id).art" : "video.card.\(id)")
        .task(id: id) {
            guard title == nil, let client else { return }
            loaded = try? await client.video(id).displayTitle
        }
    }
}
