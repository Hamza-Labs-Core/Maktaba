import MaktabaKit
import SwiftUI

/// Libraries, their videos, a video. A NavigationSplitView: Apple's guidance for iPhone Duo
/// is that it adapts to every pose by itself (one column closed, columns open), as on iPad
/// and the Mac.
struct LibraryBrowser: View {
    let session: AppSession
    let user: User
    let client: APIClient

    @State private var libraries: [Library] = []
    @State private var library: String? = Self.all
    @State private var videos: [Video] = []
    @State private var next: String?
    @State private var video: Video?
    @State private var query = ""
    @State private var problem: String?

    static let all = "all"

    var body: some View {
        NavigationSplitView {
            List(selection: $library) {
                Label("All videos", systemImage: "square.grid.2x2").tag(Self.all)
                Section("Libraries") {
                    ForEach(libraries) { lib in
                        Label(lib.name, systemImage: "books.vertical").tag(lib.id)
                    }
                }
            }
            .navigationTitle(session.current?.name ?? "Maktaba")
            .toolbar { accountMenu }
        } content: {
            List(selection: $video) {
                ForEach(videos) { v in
                    VideoRow(client: client, video: v).tag(v)
                        .onAppear { if v == videos.last { Task { await loadMore() } } }
                }
            }
            .overlay {
                if videos.isEmpty, problem == nil { ContentUnavailableView("No videos", systemImage: "film") }
                if let problem { ContentUnavailableView("Can't load", systemImage: "wifi.exclamationmark", description: Text(problem)) }
            }
            .searchable(text: $query, prompt: "Search titles")
            .navigationTitle(title)
            .refreshable { await reload() }
        } detail: {
            if let video {
                VideoDetail(client: client, video: video)
            } else {
                ContentUnavailableView("Choose a video", systemImage: "play.rectangle")
            }
        }
        .task { await loadLibraries() }
        .task(id: "\(library ?? "")|\(query)") { await reload() }
    }

    private var title: String {
        libraries.first { $0.id == library }?.name ?? "All videos"
    }

    private var accountMenu: some ToolbarContent {
        ToolbarItem(placement: .primaryAction) {
            Menu {
                Text(user.username)
                Button("Switch home", systemImage: "house") { session.switchHome() }
                Button("Sign out", systemImage: "rectangle.portrait.and.arrow.right") { session.signOut() }
            } label: {
                Label("Account", systemImage: "person.crop.circle")
            }
        }
    }

    private func loadLibraries() async {
        do {
            libraries = try await client.libraries()
        } catch APIError.signedOut {
            session.signedOut()
        } catch {
            problem = error.localizedDescription
        }
    }

    private func reload() async {
        do {
            let page = try await client.videos(library: library == Self.all ? nil : library, query: query)
            videos = page.items
            next = page.next
            problem = nil
        } catch APIError.signedOut {
            session.signedOut()
        } catch {
            problem = error.localizedDescription
        }
    }

    private func loadMore() async {
        guard let next else { return }
        self.next = nil
        if let page = try? await client.videos(library: library == Self.all ? nil : library, query: query, cursor: next) {
            videos += page.items
            self.next = page.next
        }
    }
}

struct VideoRow: View {
    let client: APIClient
    let video: Video

    var body: some View {
        HStack(spacing: 12) {
            AuthorizedImage(client: client, video: video.id)
                .frame(width: 120)
                .clipShape(.rect(cornerRadius: 6))
            VStack(alignment: .leading, spacing: 4) {
                Text(video.displayTitle).font(.headline).lineLimit(2)
                if let d = video.durationSec {
                    Text(Duration.seconds(d).formatted(.time(pattern: d >= 3600 ? .hourMinuteSecond : .minuteSecond)))
                        .font(.caption).foregroundStyle(.secondary).monospacedDigit()
                }
            }
        }
        .padding(.vertical, 4)
    }
}
