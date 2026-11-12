import MaktabaKit
import SwiftUI

/// The Profile's parental rows (Epic 57): for a sign-in the home limits, that a parent
/// manages the account and the limit (57.05), the device's limit and a looser one waiting
/// (57.04); the Age range row with "Share age range again" where Apple offers it (57.04);
/// and Family for an admin or a guardian (57.07). A viewer with no limit and no child sees
/// none of it.
struct ParentalProfileSection: View {
    let context: PlayerContext
    @State private var family = false

    private var store: ParentalStore { context.session.parental }
    private var signal: AgeSignalSender { context.session.ageSignal }

    /// Apple answered something about the age range on this sign-in (a build without the
    /// capability, or a Mac, never does: no row).
    private var hasRange: Bool {
        switch signal.range {
        case .shared, .declined: true
        case .notAvailable, nil: false
        }
    }

    var body: some View {
        let limit = store.current
        if limit.limited || hasRange || store.canManage {
            Section {
                if limit.restricted {
                    Label { Text(verbatim: ParentalWords.text("parental.profile.managed")) } icon: { Image(systemName: "person.2.badge.gearshape") }
                        .accessibilityIdentifier("profile.parental.managed")
                    if let tier = limit.maxTier {
                        LabeledContent {
                            Text(verbatim: AgeTier.label(tier))
                        } label: {
                            Label { Text(verbatim: ParentalWords.text("parental.profile.limit")) } icon: { Image(systemName: "lock") }
                        }
                        .accessibilityIdentifier("profile.parental.limit")
                    }
                }
                if let device = limit.deviceLimit {
                    Label { Text(verbatim: ParentalWords.text("ageSignal.limit.device", AgeTier.label(device))) } icon: { Image(systemName: "iphone") }
                        .accessibilityIdentifier("profile.parental.device")
                }
                if limit.devicePending || signal.answer?.pendingLooser == true {
                    Label { Text(verbatim: ParentalWords.text("ageSignal.limit.pending")) } icon: { Image(systemName: "hourglass") }
                        .accessibilityIdentifier("profile.parental.pending")
                }
                if hasRange {
                    VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                        LabeledContent {
                            Text(verbatim: signal.rangeText())
                        } label: {
                            Label { Text(verbatim: ParentalWords.text("ageSignal.profile.title")) } icon: { Image(systemName: "person.text.rectangle") }
                        }
                        Text(verbatim: ParentalWords.text("ageSignal.profile.detail"))
                            .font(.footnote)
                            .foregroundStyle(Color.mktMuted)
                            .fixedSize(horizontal: false, vertical: true)
                    }
                    .accessibilityElement(children: .combine)
                    .accessibilityIdentifier("profile.parental.ageRange")
                    Button {
                        Task { await signal.sync(askAgain: true) }
                    } label: {
                        Text(verbatim: ParentalWords.text("ageSignal.profile.askAgain")).frame(minHeight: 44)
                    }
                    .accessibilityIdentifier("profile.parental.askAgain")
                }
                if store.canManage {
                    Button {
                        family = true
                    } label: {
                        HStack {
                            Label { Text(verbatim: ParentalWords.text("family.title")) } icon: { Image(systemName: "figure.2.and.child.holdinghands") }
                            Spacer(minLength: 0)
                            Image(systemName: "chevron.forward")
                                .font(.footnote.weight(.semibold))
                                .foregroundStyle(.tertiary)
                                .accessibilityHidden(true)
                        }
                        .contentShape(Rectangle())
                    }
                    .foregroundStyle(Color.mktText)
                    .accessibilityIdentifier("profile.family")
                }
            }
            .sheet(isPresented: $family) {
                FamilyView(context: context)
                    #if os(macOS)
                    .frame(minWidth: 520, minHeight: 560)
                    #endif
            }
        }
    }
}
