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
    /// The home's switch as an admin reads it (story 57.12): its child-device notice shows
    /// above Family until an admin dismisses it.
    @State private var settings: ParentalSettings?

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
                    IconLine(ParentalWords.text("parental.profile.managed"), systemImage: "person.2.badge.gearshape")
                        .accessibilityIdentifier("profile.parental.managed")
                    if let tier = limit.maxTier {
                        LabeledContent {
                            Text(verbatim: AgeTier.label(tier))
                        } label: {
                            Label { Text(verbatim: ParentalWords.text("parental.profile.limit")) } icon: { Image(systemName: "lock").accessibilityHidden(true) }
                        }
                        .accessibilityElement(children: .combine)
                        .accessibilityIdentifier("profile.parental.limit")
                    }
                }
                if let device = limit.deviceLimit {
                    IconLine(ParentalWords.text("ageSignal.limit.device", AgeTier.label(device)), systemImage: "iphone")
                        .accessibilityIdentifier("profile.parental.device")
                }
                if limit.devicePending || signal.answer?.pendingLooser == true {
                    IconLine(ParentalWords.text("ageSignal.limit.pending"), systemImage: "hourglass")
                        .accessibilityIdentifier("profile.parental.pending")
                }
                if hasRange {
                    VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                        LabeledContent {
                            Text(verbatim: signal.rangeText())
                        } label: {
                            Label { Text(verbatim: ParentalWords.text("ageSignal.profile.title")) } icon: { Image(systemName: "person.text.rectangle").accessibilityHidden(true) }
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
                    if context.user.isAdmin, settings?.childDeviceNotice == true {
                        // a signed-in device says a child uses it, and parental controls
                        // are off: said here, set up in Family
                        ChildDeviceNoticeText()
                    }
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
                    // on the Family row alone: a modifier on the section goes to each of its
                    // rows, and two rows presenting one sheet close it as it opens
                    .sheet(isPresented: $family) {
                        FamilyView(context: context)
                            #if os(macOS)
                            .frame(minWidth: 520, minHeight: 560)
                            #endif
                    }
                    // read when Profile shows and when Family closes (the notice was
                    // dismissed, or parental controls were set up)
                    .task(id: family) { await readSettings() }
                }
            }
        }
    }

    /// The home's switch, for an admin only (anyone else is refused by the home).
    private func readSettings() async {
        guard !family, context.user.isAdmin, store.canManage else { return }
        if let s = try? await context.client.parentalSettings() { settings = s }
    }
}
