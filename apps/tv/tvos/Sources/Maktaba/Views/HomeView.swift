import SwiftUI

public struct HomeView: View {
    @State private var continueRow: [VideoSummary] = []
    @State private var recommendations: [VideoSummary] = []

    public init() {}

    public var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 48) {
                if !continueRow.isEmpty {
                    Section(header: rowHeader("Continue Watching")) {
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
                    PosterCard(item: item, showProgress: showProgress)
                }
            }
        }
    }

    private func reload() async {
        let svc = LibraryService()
        continueRow = (try? await svc.continueWatching()) ?? []
        recommendations = (try? await svc.recommendations()) ?? []
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
                        ProgressView(value: item.progressFraction)
                            .padding(.horizontal, 16)
                            .padding(.bottom, 8)
                            .frame(maxHeight: .infinity, alignment: .bottom)
                    }
                }
            Text(item.title)
                .font(.headline)
                .lineLimit(2)
                .frame(width: 320, alignment: .leading)
        }
    }
}
