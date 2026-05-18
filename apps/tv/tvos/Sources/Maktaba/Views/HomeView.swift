#if os(tvOS)
import SwiftUI

public struct HomeView: View {
    @EnvironmentObject var session: AppSession
    @State private var continueRow: [VideoSummary] = []
    @State private var recommendations: [VideoSummary] = []
    @State private var loadError: String?

    public init() {}

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 48) {
                if let loadError = loadError {
                    Text(loadError)
                        .font(.headline)
                        .foregroundColor(.orange)
                }
                Section(header: rowHeader("Continue Watching")) {
                    if continueRow.isEmpty {
                        Text("Nothing in progress yet — start a video to see it here.")
                            .font(.headline)
                            .foregroundColor(.secondary)
                    } else {
                        rowOfPosters(continueRow, showProgress: true)
                    }
                }
                if !recommendations.isEmpty {
                    Section(header: rowHeader("Recommended for You")) {
                        rowOfPosters(recommendations, showProgress: false)
                    }
                }
            }
            .padding(.horizontal, 96)
        }
        .task { await reload() }
    }

    private func rowHeader(_ title: String) -> some View {
        Text(title).font(.title2.bold())
    }

    private func rowOfPosters(_ items: [VideoSummary], showProgress: Bool) -> some View {
        ScrollView(.horizontal, showsIndicators: false) {
            HStack(spacing: 32) {
                ForEach(items) { item in
                    Button(action: {}) {
                        PosterCard(item: item, showProgress: showProgress)
                    }
                    .buttonStyle(.card)
                }
            }
        }
    }

    private func reload() async {
        guard let cfg = session.apiConfig else {
            loadError = "Not paired with a server."
            return
        }
        let svc = LibraryService(gql: GraphQLClient(config: cfg))
        do {
            continueRow = try await svc.continueWatching()
            recommendations = try await svc.recommendations()
            loadError = nil
        } catch {
            // EC: surface a banner instead of silently swallowing.
            loadError = "Showing cached rows — couldn't reach the server."
        }
    }
}

public struct PosterCard: View {
    let item: VideoSummary
    let showProgress: Bool

    public var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            Rectangle()
                .fill(Color.gray.opacity(0.3))
                .frame(width: 320, height: 180)
                .overlay {
                    if showProgress {
                        VStack {
                            Spacer()
                            Text(remainingLabel)
                                .font(.caption)
                                .foregroundColor(.white)
                            ProgressView(value: item.progressFraction)
                                .padding(.horizontal, 16)
                                .padding(.bottom, 8)
                        }
                    }
                }
            Text(item.title)
                .font(.headline)
                .lineLimit(2)
                .frame(width: 320, alignment: .leading)
        }
    }

    private var remainingLabel: String {
        let mins = Int(item.remainingSec / 60)
        return "\(mins) min left"
    }
}
#endif
