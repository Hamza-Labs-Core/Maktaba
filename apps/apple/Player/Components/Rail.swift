import MaktabaKit
import SwiftUI

/// A titled row that scrolls sideways (Home's and a library's rows): the title as a heading,
/// "See all" when there is more, then the cards, snapping to each. In Arabic the row starts
/// on the right (NFR-11); at the accessibility sizes the title and "See all" stack and the
/// cards grow (NFR-7).
struct Rail<Items: RandomAccessCollection, Card: View>: View where Items.Element: Identifiable {
    let title: Text
    let items: Items
    var seeAll: (() -> Void)?
    @ViewBuilder let card: (Items.Element) -> Card

    @Environment(\.dynamicTypeSize) private var typeSize

    init(_ title: Text, items: Items, seeAll: (() -> Void)? = nil, @ViewBuilder card: @escaping (Items.Element) -> Card) {
        self.title = title
        self.items = items
        self.seeAll = seeAll
        self.card = card
    }

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s3) {
            header
                .padding(.horizontal, MaktabaTokens.Spacing.s4)
            ScrollView(.horizontal) {
                LazyHStack(alignment: .top, spacing: MaktabaTokens.Spacing.s3) {
                    ForEach(items) { card($0) }
                }
                .scrollTargetLayout()
            }
            .contentMargins(.horizontal, MaktabaTokens.Spacing.s4, for: .scrollContent)
            .scrollTargetBehavior(.viewAligned)
            .scrollIndicators(.hidden)
            .accessibilityIdentifier("rail.cards")
        }
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("rail")
    }

    private var header: some View {
        let layout = typeSize.isAccessibilitySize
            ? AnyLayout(VStackLayout(alignment: .leading, spacing: MaktabaTokens.Spacing.s1))
            : AnyLayout(HStackLayout(alignment: .firstTextBaseline, spacing: MaktabaTokens.Spacing.s2))
        return layout {
            title
                .font(.maktabaHeading(MaktabaTokens.TypeSize.h2, relativeTo: .title2))
                .foregroundStyle(Color.mktText)
                .fixedSize(horizontal: false, vertical: true)
                .accessibilityAddTraits(.isHeader)
                .accessibilityIdentifier("rail.title")
            if !typeSize.isAccessibilitySize { Spacer(minLength: MaktabaTokens.Spacing.s2) }
            if let seeAll {
                Button(action: seeAll) {
                    Text("See all", tableName: "Components", comment: "A row's link to all of its items")
                        .font(.maktabaBody(15, weight: .medium, relativeTo: .subheadline))
                        .foregroundStyle(Color.mktBrassText)
                        .frame(minHeight: 44)
                        .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
                .accessibilityIdentifier("rail.seeAll")
            }
        }
    }
}

#if DEBUG
#Preview("Rail, dark") {
    ComponentGallery(piece: .rail).preferredColorScheme(.dark)
}

#Preview("Rail, Arabic, AX3") {
    ComponentGallery(piece: .rail).galleryPreview(arabic: true, large: true)
}
#endif
