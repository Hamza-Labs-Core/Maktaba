import MaktabaKit
import SwiftUI

// Epic 57 story 57.07: Profile > Family, for the home's admins and for guardians (shown only
// when the home says the caller looks after someone, `ParentalStore.canManage`). The home
// enforces every rule: these screens call its routes and say what it answered. The words are
// MaktabaKit's (`ParentalWords`: English and Arabic under the epic's keys).

private func word(_ key: String, _ args: CVarArg...) -> String {
    let format = ParentalWords.text(key)
    return args.isEmpty ? format : String(format: format, locale: AppLanguage.locale, arguments: args)
}

extension View {
    /// What a Family screen says after a change the home refused.
    func familyNotice(_ notice: Binding<FamilyNotice?>) -> some View {
        alert(Text(verbatim: notice.wrappedValue?.line() ?? ""), isPresented: Binding { notice.wrappedValue != nil } set: { if !$0 { notice.wrappedValue = nil } }) {
            Button(role: .cancel) {} label: { Text(verbatim: word("family.ok")) }
        }
    }
}

extension EnvironmentValues {
    /// A play-only sign-in through Maktaba Cloud (`AppSession.playOnly`): the home refuses
    /// its every change, so the Family screens show everything and change nothing.
    @Entry var familyReadOnly = false
}

/// A control that changes something on the home: disabled for a play-only sign-in.
private struct FamilyChange: ViewModifier {
    @Environment(\.familyReadOnly) private var readOnly

    func body(content: Content) -> some View {
        content.disabled(readOnly)
    }
}

extension View {
    func familyChange() -> some View { modifier(FamilyChange()) }
}

/// Why nothing can be changed here, said before anything is tried (a play-only sign-in).
struct FamilyReadOnlyNote: View {
    @Environment(\.familyReadOnly) private var readOnly

    var body: some View {
        if readOnly {
            Section {
                IconLine(word("family.readOnlyCloud"), systemImage: "lock")
                    .foregroundStyle(Color.mktMuted)
                    .fixedSize(horizontal: false, vertical: true)
                    .accessibilityIdentifier("family.readOnly")
            }
        }
    }
}

/// Family: the children, the requests waiting, the parent PIN; for an admin, Rate libraries.
struct FamilyView: View {
    let context: PlayerContext
    @State private var model: FamilyModel
    @State private var restricting = false
    @Environment(\.dismiss) private var dismiss

    init(context: PlayerContext) {
        self.context = context
        _model = State(initialValue: FamilyModel(client: context.client, isAdmin: context.user.isAdmin))
    }

    var body: some View {
        NavigationStack {
            List {
                FamilyReadOnlyNote()
                Section {
                    switch model.children {
                    case .loading:
                        ProgressView().frame(maxWidth: .infinity)
                    case let .failed(failure):
                        Text(verbatim: failure.message(video: false)).foregroundStyle(Color.mktMuted)
                            .accessibilityIdentifier("family.failed")
                    case let .ready(children) where children.isEmpty:
                        Text(verbatim: word("family.none")).foregroundStyle(Color.mktMuted)
                            .accessibilityIdentifier("family.none")
                    case let .ready(children):
                        ForEach(children) { child in
                            NavigationLink(value: ParentalPerson(id: child.id, username: child.username)) {
                                HStack {
                                    Label { Text(verbatim: child.username) } icon: { Image(systemName: "person.crop.circle") }
                                    Spacer(minLength: MaktabaTokens.Spacing.s2)
                                    Text(verbatim: AgeTier.label(child.policy.maxTier)).foregroundStyle(Color.mktMuted)
                                    let waiting = model.waiting(for: child.id)
                                    if waiting > 0 {
                                        Text(verbatim: word("family.requests.waiting", waiting))
                                            .font(.footnote.weight(.semibold))
                                            .padding(.horizontal, 8)
                                            .padding(.vertical, 2)
                                            .background(Color.brass, in: .capsule)
                                            .foregroundStyle(MaktabaTokens.Colors.accentFg.color)
                                    }
                                }
                                .frame(minHeight: 44)
                            }
                            .accessibilityIdentifier("family.child.\(child.id)")
                        }
                    }
                    // an admin makes an account restricted: one of the home's accounts
                    // (`GET /api/admin/library-acl`), and its limit
                    if model.isAdmin, !model.restrictable(me: context.user.id).isEmpty {
                        Button { restricting = true } label: {
                            Label { Text(verbatim: word("family.makeRestricted")) } icon: { Image(systemName: "person.badge.plus") }
                                .frame(minHeight: 44)
                        }
                        .accessibilityIdentifier("family.makeRestricted")
                        .familyChange()
                    }
                } header: {
                    header("family.children")
                }

                Section {
                    if model.requests.isEmpty {
                        Text(verbatim: word("family.requests.none")).foregroundStyle(Color.mktMuted)
                            .accessibilityIdentifier("family.requests.none")
                    }
                    ForEach(model.requests) { item in RequestRow(item: item, model: model) }
                } header: {
                    header("family.requests")
                }

                Section {
                    NavigationLink {
                        PinView(model: model)
                    } label: {
                        LabeledContent {
                            if let set = model.pinSet { Text(verbatim: word(set ? "family.pin.isSet" : "family.pin.notSet")) }
                        } label: {
                            Label { Text(verbatim: word("family.pin")) } icon: { Image(systemName: "lock.shield") }
                        }
                        .frame(minHeight: 44)
                    }
                    .accessibilityIdentifier("family.pin")
                } footer: {
                    Text(verbatim: word("family.pin.detail"))
                }

                if model.isAdmin {
                    Section {
                        NavigationLink {
                            RateLibrariesView(client: context.client)
                        } label: {
                            Label { Text(verbatim: word("family.rateLibraries")) } icon: { Image(systemName: "books.vertical") }
                                .frame(minHeight: 44)
                        }
                        .accessibilityIdentifier("family.rateLibraries")
                    }
                }
            }
            .maktabaSurface()
            .navigationTitle(Text(verbatim: word("family.title")))
            .navigationDestination(for: ParentalPerson.self) { child in
                ChildView(context: context, child: child, accounts: model.accounts,
                          restricted: Set((model.children.value ?? []).map(\.id))) { Task { await model.load() } }
            }
            .sheet(isPresented: $restricting) {
                RestrictAccountSheet(model: model, me: context.user.id) { restricting = false }
            }
            .toolbar {
                ToolbarItem(placement: .confirmationAction) {
                    Button { dismiss() } label: { Text(verbatim: word("family.done")) }
                        .accessibilityIdentifier("family.done")
                }
            }
            .refreshable { await model.load() }
            .task { await model.load() }
            .familyNotice($model.notice)
            .accessibilityIdentifier("screen.family")
        }
        // a play-only sign-in through Maktaba Cloud changes nothing: said and disabled up front
        .environment(\.familyReadOnly, context.session.playOnly)
    }

    private func header(_ key: String) -> some View {
        Text(verbatim: word(key)).accessibilityAddTraits(.isHeader)
    }
}

/// Make an account restricted (admins): the home's accounts that are not restricted yet;
/// choosing one's limit sends it. The home refuses an admin's account, and the sheet says so.
private struct RestrictAccountSheet: View {
    let model: FamilyModel
    let me: String
    let done: () -> Void

    var body: some View {
        NavigationStack {
            List {
                Section {
                    ForEach(model.restrictable(me: me)) { person in
                        // choosing the limit sends it (a menu's items carry their identifiers
                        // only when they are its direct children)
                        Menu {
                            ForEach(AgeTier.limits, id: \.self) { tier in
                                Button {
                                    Task { if await model.restrict(person, maxTier: tier) { done() } }
                                } label: {
                                    Text(verbatim: AgeTier.label(tier))
                                }
                                .accessibilityIdentifier("restrict.tier.\(tier)")
                            }
                        } label: {
                            Label { Text(verbatim: person.username).foregroundStyle(Color.mktText) } icon: { Image(systemName: "person.crop.circle") }
                                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                        }
                        .accessibilityIdentifier("restrict.account.\(person.id)")
                    }
                } header: {
                    Text(verbatim: word("family.child.limit")).accessibilityAddTraits(.isHeader)
                }
            }
            .maktabaSurface()
            .navigationTitle(Text(verbatim: word("family.makeRestricted")))
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button(action: done) { Text(verbatim: word("parental.pin.cancel")) }
                        .accessibilityIdentifier("restrict.cancel")
                }
            }
            .familyNotice(Binding { model.notice } set: { model.notice = $0 })
            .accessibilityIdentifier("screen.restrict")
        }
    }
}

/// Add a guardian (admins): the home's accounts that can look after this child; choosing one
/// sends it.
private struct GuardianPicker: View {
    let candidates: [ParentalPerson]
    /// The account chosen (nil: cancelled).
    let done: (ParentalPerson?) -> Void

    var body: some View {
        NavigationStack {
            List {
                ForEach(candidates) { person in
                    Button { done(person) } label: {
                        Label { Text(verbatim: person.username).foregroundStyle(Color.mktText) } icon: { Image(systemName: "person.crop.circle") }
                            .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                    }
                    .accessibilityIdentifier("child.guardian.add.\(person.id)")
                }
            }
            .maktabaSurface()
            .navigationTitle(Text(verbatim: word("family.guardians.add")))
            #if os(iOS)
            .navigationBarTitleDisplayMode(.inline)
            #endif
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { done(nil) } label: { Text(verbatim: word("parental.pin.cancel")) }
                        .accessibilityIdentifier("child.guardian.add.cancel")
                }
            }
            .accessibilityIdentifier("screen.addGuardian")
        }
    }
}

/// One waiting request: who asks for what (the guardian may see its title and tier), when,
/// and Approve (today, this week, always) or Deny.
private struct RequestRow: View {
    let item: ParentalRequestItem
    let model: FamilyModel

    var body: some View {
        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
            HStack(alignment: .top, spacing: MaktabaTokens.Spacing.s3) {
                // the poster of a video the guardian may see (the home sends none otherwise)
                if let video = item.posterVideo {
                    AuthorizedImage(client: model.client, video: video, small: true)
                        .frame(width: 96, height: 54)
                        .clipShape(.rect(cornerRadius: MaktabaTokens.Radius.sm))
                        .accessibilityHidden(true)
                        .accessibilityIdentifier("family.request.poster.\(item.id)")
                }
                VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                    Text(verbatim: item.line())
                        .foregroundStyle(Color.mktText)
                        .fixedSize(horizontal: false, vertical: true)
                    HStack(spacing: MaktabaTokens.Spacing.s2) {
                        if !item.request.isTime { Text(verbatim: AgeTier.contentLabel(item.subject.tier)) }
                        if let at = item.request.createdAt {
                            Text(at, format: .relative(presentation: .named))
                        }
                    }
                    .font(.footnote)
                    .foregroundStyle(Color.mktMuted)
                }
            }
            HStack(spacing: MaktabaTokens.Spacing.s3) {
                Group {
                    if item.request.isTime {
                        // more time is for today: one tap gives the minutes asked
                        Button { Task { await model.approve(item, for: .today) } } label: {
                            Text(verbatim: word("family.device.approve")).frame(minWidth: 88, minHeight: 44)
                        }
                    } else {
                        Menu {
                            ForEach(ApprovalLength.allCases, id: \.self) { length in
                                Button { Task { await model.approve(item, for: length) } } label: { Text(verbatim: word(length.key)) }
                                    .accessibilityIdentifier("family.request.approve.\(length.rawValue)")
                            }
                        } label: {
                            Text(verbatim: word("family.device.approve")).frame(minWidth: 88, minHeight: 44)
                        }
                    }
                }
                .buttonStyle(.borderedProminent)
                .tint(Color.brass)
                .foregroundStyle(MaktabaTokens.Colors.accentFg.color)
                .accessibilityIdentifier("family.request.approve.\(item.id)")
                .familyChange()
                Button { Task { await model.deny(item) } } label: {
                    Text(verbatim: word("family.deny")).frame(minWidth: 88, minHeight: 44)
                }
                .buttonStyle(.bordered)
                .accessibilityIdentifier("family.request.deny.\(item.id)")
                .familyChange()
            }
        }
        .padding(.vertical, MaktabaTokens.Spacing.s1)
        .accessibilityElement(children: .contain)
        .accessibilityIdentifier("family.request.\(item.id)")
    }
}

/// Set or change the caller's parent PIN: six digits, confirmed by the account's password.
struct PinView: View {
    let model: FamilyModel
    @State private var pin = ""
    @State private var password = ""
    @State private var saving = false
    @State private var saved = false
    @State private var invalid = false

    var body: some View {
        Form {
            FamilyReadOnlyNote()
            Section {
                SecureField(text: $pin) { Text(verbatim: word("family.pin.new")) }
                    #if os(iOS)
                    .keyboardType(.numberPad)
                    .textContentType(.oneTimeCode)
                    #endif
                    // the digits read left to right in Arabic too
                    .environment(\.layoutDirection, .leftToRight)
                    .accessibilityIdentifier("family.pin.new")
                    .familyChange()
                SecureField(text: $password) { Text(verbatim: word("family.pin.password")) }
                    #if os(iOS)
                    .textContentType(.password)
                    #endif
                    .accessibilityIdentifier("family.pin.password")
                    .familyChange()
            } footer: {
                if invalid {
                    Text(verbatim: word("parental.pin.enter")).foregroundStyle(MaktabaTokens.Colors.error.color)
                } else if saved {
                    Text(verbatim: word("family.pin.saved")).accessibilityIdentifier("family.pin.saved")
                } else {
                    Text(verbatim: word("family.pin.detail"))
                }
            }
            Section {
                Button {
                    Task { await save() }
                } label: {
                    Text(verbatim: word("family.pin.save")).frame(maxWidth: .infinity, minHeight: 44)
                }
                .disabled(saving || pin.isEmpty || password.isEmpty)
                .accessibilityIdentifier("family.pin.save")
                .familyChange()
            }
        }
        .maktabaSurface()
        .navigationTitle(Text(verbatim: word(model.pinSet == true ? "family.pin.change" : "family.pin.set")))
        .familyNotice(Binding { model.notice } set: { model.notice = $0 })
    }

    private func save() async {
        saved = false
        invalid = PinPad.normalize(pin) == nil
        guard !invalid else { return }
        saving = true
        defer { saving = false }
        if await model.setPIN(pin, password: password) {
            saved = true
            pin = ""
            password = ""
        }
    }
}

/// Rate libraries (admins): each library's default tier, which every video in it with no
/// rating of its own takes.
struct RateLibrariesView: View {
    let client: APIClient
    @State private var libraries: Load<[LibrarySummary]> = .loading
    /// What was set here (the home's library list does not say a library's tier).
    @State private var chosen: [String: Int?] = [:]
    @State private var notice: FamilyNotice?

    var body: some View {
        List {
            Section {
                switch libraries {
                case .loading: ProgressView().frame(maxWidth: .infinity)
                case let .failed(failure):
                    Text(verbatim: failure.message(video: false)).foregroundStyle(Color.mktMuted)
                        .accessibilityIdentifier("rateLibraries.failed")
                case let .ready(list):
                    ForEach(list) { library in
                        Menu {
                            ForEach(AgeTier.content, id: \.self) { tier in
                                Button { Task { await set(library, tier) } } label: { Text(verbatim: AgeTier.label(tier)) }
                            }
                            Button { Task { await set(library, nil) } } label: { Text(verbatim: word("family.rateLibraries.notRated")) }
                        } label: {
                            LabeledContent {
                                if let set = chosen[library.id] {
                                    Text(verbatim: set.map { AgeTier.label($0) } ?? word("family.rateLibraries.notRated"))
                                }
                            } label: {
                                Text(verbatim: library.name).foregroundStyle(Color.mktText)
                            }
                            .frame(minHeight: 44)
                        }
                        .accessibilityIdentifier("family.rate.\(library.id)")
                    }
                }
            } footer: {
                Text(verbatim: word("family.rateLibraries.detail"))
            }
        }
        .maktabaSurface()
        .navigationTitle(Text(verbatim: word("family.rateLibraries")))
        .familyNotice($notice)
        .task {
            do {
                libraries = .ready(try await client.librarySummaries())
            } catch {
                if !Log.isCancellation(error) { libraries = .failed(HomeFailure(error)) }
            }
            // the tiers the home has stored, where it says them (a newer home, to an admin)
            if let stored = try? await client.libraryAgeTiers() {
                for (id, tier) in stored where chosen[id] == nil { chosen[id] = tier }
            }
        }
    }

    private func set(_ library: LibrarySummary, _ tier: Int?) async {
        do {
            try await client.setLibraryAgeTier(library.id, tier)
            chosen[library.id] = .some(tier)
        } catch {
            if !Log.isCancellation(error) { notice = FamilyNotice(error) }
        }
    }
}

/// A child's page: the limit, what is allowed and blocked, the devices, the time, the
/// activity; for an admin, the guardians and Lift restriction.
struct ChildView: View {
    let context: PlayerContext
    let child: ParentalPerson
    /// The home's accounts (an admin's list; empty for a guardian) and which are restricted:
    /// who can be named a guardian.
    var accounts: [ParentalPerson] = []
    var restricted: Set<String> = []
    /// The limit changed: the Family list reads again.
    let changed: () -> Void

    @State private var model: ChildModel
    @State private var confirmLift = false
    @State private var addingGuardian = false
    @Environment(\.dismiss) private var dismiss

    init(context: PlayerContext, child: ParentalPerson, accounts: [ParentalPerson] = [], restricted: Set<String> = [],
         changed: @escaping () -> Void) {
        self.context = context
        self.child = child
        self.accounts = accounts
        self.restricted = restricted
        self.changed = changed
        _model = State(initialValue: ChildModel(client: context.client, child: child))
    }

    var body: some View {
        List {
            switch model.account {
            case .loading:
                ProgressView().frame(maxWidth: .infinity)
            case let .failed(failure):
                Text(verbatim: failure.message(video: false)).foregroundStyle(Color.mktMuted)
            case let .ready(account):
                FamilyReadOnlyNote()
                limit(account.policy).familyChange()
                Section {
                    link("family.child.allowed", "checkmark.circle", count: model.rules("allow").count) { RulesView(context: context, model: model, effect: "allow") }
                        .accessibilityIdentifier("child.allowed")
                    link("family.child.blocked", "nosign", count: model.rules("block").count) { RulesView(context: context, model: model, effect: "block") }
                        .accessibilityIdentifier("child.blocked")
                    link("family.child.devices", "iphone", count: account.signals.count) { DevicesView(model: model) }
                        .accessibilityIdentifier("child.devices")
                    link("parental.time.rules", "clock", count: model.timeRules.count) { TimeRulesView(model: model) }
                        .accessibilityIdentifier("child.time")
                    link("family.child.activity", "list.bullet.rectangle", count: nil) { ActivityView(client: context.client, child: child, usage: model.usage, libraries: model.libraries) }
                        .accessibilityIdentifier("child.activity")
                }
                Section {
                    ForEach(account.guardians) { guardian in
                        HStack {
                            Label { Text(verbatim: guardian.username) } icon: { Image(systemName: "person.badge.shield.checkmark").accessibilityHidden(true) }
                            Spacer()
                            // naming and removing guardians is an admin's (the home refuses a guardian)
                            if account.isAdmin {
                                Button { Task { await model.removeGuardian(guardian) } } label: { Text(verbatim: word("family.rules.remove")) }
                                    .buttonStyle(.borderless)
                                    .accessibilityIdentifier("child.guardian.remove.\(guardian.id)")
                                    .familyChange()
                            }
                        }
                        .frame(minHeight: 44)
                    }
                    // naming a guardian is an admin's: one of the home's other accounts
                    let candidates = model.guardianCandidates(accounts, restricted: restricted)
                    if account.isAdmin, !candidates.isEmpty {
                        // a sheet with the accounts, not a menu in the row: the page is still
                        // reading its rules and time when this shows, and each answer drew
                        // the row again and closed a menu that had just opened. The sheet
                        // hangs on the page, not on this row, so a row drawn again keeps it
                        Button { addingGuardian = true } label: {
                            Label { Text(verbatim: word("family.guardians.add")) } icon: { Image(systemName: "plus") }
                                .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                        }
                        .accessibilityIdentifier("child.guardian.add")
                        .familyChange()
                    }
                } header: {
                    Text(verbatim: word("family.child.guardians")).accessibilityAddTraits(.isHeader)
                }
                if account.isAdmin {
                    Section {
                        Button { confirmLift = true } label: {
                            Text(verbatim: word("family.child.lift")).frame(maxWidth: .infinity, minHeight: 44)
                        }
                        .foregroundStyle(Color.red)
                        .accessibilityIdentifier("child.lift")
                        .familyChange()
                    }
                }
            }
        }
        .maktabaSurface()
        .navigationTitle(Text(verbatim: child.username))
        .task { await model.load() }
        .refreshable { await model.load() }
        .familyNotice(Binding { model.notice } set: { model.notice = $0 })
        .sheet(isPresented: $addingGuardian) {
            GuardianPicker(candidates: model.guardianCandidates(accounts, restricted: restricted)) { person in
                addingGuardian = false
                if let person { Task { await model.addGuardian(person) } }
            }
        }
        .confirmationDialog(Text(verbatim: word("family.child.lift.confirm", child.username)), isPresented: $confirmLift, titleVisibility: .visible) {
            Button(role: .destructive) {
                Task { await model.lift() }
            } label: {
                Text(verbatim: word("family.child.lift"))
            }
            .accessibilityIdentifier("child.lift.confirm")
        } message: {
            Text(verbatim: word("family.child.lift.activity"))
        }
        .onChange(of: model.lifted) { _, lifted in
            guard lifted else { return }
            changed()
            dismiss()
        }
        .onChange(of: model.policy) { _, _ in changed() }
        .accessibilityIdentifier("screen.child")
    }

    /// The limit, in ascending age in both languages; a change is sent at once with the
    /// version last read.
    @ViewBuilder private func limit(_ policy: ParentalPolicy) -> some View {
        Section {
            Picker(selection: Binding { policy.maxTier } set: { tier in Task { await model.setLimit(maxTier: tier) } }) {
                ForEach(AgeTier.limits, id: \.self) { Text(verbatim: AgeTier.label($0)).tag($0) }
            } label: {
                Text(verbatim: word("family.child.limit"))
            }
            .accessibilityIdentifier("child.limit")
            Picker(selection: Binding { policy.unrated } set: { rule in Task { await model.setLimit(unrated: rule) } }) {
                ForEach(ParentalPolicy.unratedChoices, id: \.self) { Text(verbatim: ParentalPolicy.unratedLabel($0)).tag($0) }
            } label: {
                Text(verbatim: word("family.child.unrated"))
            }
            .accessibilityIdentifier("child.unrated")
            Toggle(isOn: Binding { policy.allowOnly } set: { on in Task { await model.setLimit(allowOnly: on) } }) {
                Text(verbatim: word("family.child.allowOnly"))
            }
            .accessibilityIdentifier("child.allowOnly")
        }
    }

    private func link<D: View>(_ key: String, _ image: String, count: Int?, @ViewBuilder to: @escaping () -> D) -> some View {
        NavigationLink(destination: to) {
            LabeledContent {
                if let count, count > 0 { Text(count, format: .number) }
            } label: {
                Label { Text(verbatim: word(key)) } icon: { Image(systemName: image) }
            }
            .frame(minHeight: 44)
        }
    }
}

/// Allowed or Blocked: the rules, each with what it names and when it ends; Add picks a
/// library, a collection, a series, a channel, a YouTube source, a plugin or a video from
/// what the guardian's own account sees (`RuleChoices`, MaktabaKit).
struct RulesView: View {
    let context: PlayerContext
    let model: ChildModel
    /// `allow` or `block`.
    let effect: String
    @State private var adding: String?

    var body: some View {
        List {
            let rules = model.rules(effect)
            if rules.isEmpty {
                Text(verbatim: word("family.rules.none")).foregroundStyle(Color.mktMuted)
                    .accessibilityIdentifier("rules.none")
            }
            ForEach(rules) { rule in
                HStack {
                    Label {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(verbatim: model.title(of: rule))
                            if let until = rule.expiresAt {
                                Text(verbatim: word("family.rules.until", until.formatted(date: .abbreviated, time: .shortened)))
                                    .font(.footnote)
                                    .foregroundStyle(Color.mktMuted)
                            }
                        }
                    } icon: {
                        Image(systemName: Self.symbol(rule.subjectKind)).accessibilityHidden(true)
                    }
                    Spacer()
                    Button { Task { await model.remove(rule) } } label: { Text(verbatim: word("family.rules.remove")) }
                        .buttonStyle(.borderless)
                        .accessibilityIdentifier("rules.remove.\(rule.id)")
                        .familyChange()
                }
                .frame(minHeight: 44)
                .accessibilityIdentifier("rules.rule.\(rule.id)")
            }
        }
        .maktabaSurface()
        .navigationTitle(Text(verbatim: word(effect == "allow" ? "family.child.allowed" : "family.child.blocked")))
        .toolbar {
            ToolbarItem(placement: .primaryAction) {
                Menu {
                    ForEach(RuleChoices.kinds, id: \.self) { kind in
                        Button { adding = kind } label: {
                            Label { Text(verbatim: word("family.subject.\(kind)")) } icon: { Image(systemName: Self.symbol(kind)) }
                        }
                        .accessibilityIdentifier("rules.add.\(kind)")
                    }
                } label: {
                    Label { Text(verbatim: word("family.rules.add")) } icon: { Image(systemName: "plus") }
                }
                .accessibilityIdentifier("rules.add")
                .familyChange()
            }
        }
        .sheet(item: Binding { adding.map(PickedKind.init) } set: { adding = $0?.kind }) { picked in
            SubjectPicker(client: context.client, kind: picked.kind) { subject in
                adding = nil
                Task { await model.addRule(effect, subject) }
            }
        }
        .familyNotice(Binding { model.notice } set: { model.notice = $0 })
    }

    private struct PickedKind: Identifiable {
        let kind: String
        var id: String { kind }
    }

    static func symbol(_ kind: String) -> String {
        switch kind {
        case "library": "books.vertical"
        case "collection": "rectangle.stack"
        case "series": "square.stack.3d.down.right"
        case "channel": "dot.radiowaves.left.and.right"
        case "youtube_source": "play.rectangle"
        case "plugin": "puzzlepiece.extension"
        default: "film"
        }
    }
}

/// Picks one thing of a kind from what the guardian's own account sees.
private struct SubjectPicker: View {
    let client: APIClient
    let kind: String
    let picked: (ParentalSubject) -> Void

    @State private var rows: Load<[RuleChoice]> = .loading
    @State private var query = ""
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            List {
                switch rows {
                case .loading: ProgressView().frame(maxWidth: .infinity)
                case let .failed(failure):
                    Text(verbatim: failure.message(video: false)).foregroundStyle(Color.mktMuted)
                        .accessibilityIdentifier("picker.failed")
                case let .ready(list):
                    if list.isEmpty { Text(verbatim: word("family.rules.none")).foregroundStyle(Color.mktMuted) }
                    ForEach(list) { row in
                        Button { picked(ParentalSubject(kind: kind, id: row.id)) } label: {
                            Text(verbatim: row.title).frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                        }
                        .foregroundStyle(Color.mktText)
                        .accessibilityIdentifier("picker.row.\(row.id)")
                    }
                }
            }
            .maktabaSurface()
            .navigationTitle(Text(verbatim: word("family.subject.\(kind)")))
            .searchable(text: $query, prompt: Text(verbatim: word("family.rules.search")))
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { dismiss() } label: { Text(verbatim: word("parental.pin.cancel")) }
                }
            }
            .task(id: kind == "video" ? query : "") { await load() }
        }
    }

    private func load() async {
        do {
            // a video is searched for as it is typed; the other kinds are listed whole
            if kind == "video", query.trimmingCharacters(in: .whitespacesAndNewlines).count >= 2 {
                try await Task.sleep(for: .milliseconds(350))
            }
            rows = .ready(try await RuleChoices.load(client, kind: kind, query: query))
        } catch {
            if !Log.isCancellation(error) { rows = .failed(HomeFailure(error)) }
        }
    }
}

/// Devices: each of the child's sign-ins whose device said something about age: its limit,
/// where it came from, and a looser limit waiting with Approve.
struct DevicesView: View {
    let model: ChildModel

    var body: some View {
        List {
            let signals = model.account.value?.signals ?? []
            if signals.isEmpty {
                Text(verbatim: word("family.device.none")).foregroundStyle(Color.mktMuted)
                    .accessibilityIdentifier("devices.none")
            }
            ForEach(signals) { signal in
                Section {
                    Text(verbatim: signal.tierLimit.map { word("family.device.limit", AgeTier.label($0)) } ?? word("family.device.noLimit"))
                        .accessibilityIdentifier("devices.limit.\(signal.id)")
                    if signal.fromAgeRange {
                        LabeledContent {
                            Text(verbatim: "\u{200E}\(signal.lower.map(String.init) ?? "0")–\(signal.upper.map(String.init) ?? "+")")
                        } label: {
                            Text(verbatim: word("family.device.ageRange"))
                        }
                    }
                    if signal.fromScreenTime {
                        Text(verbatim: word("family.device.screenTime"))
                    }
                    if let pending = signal.pendingLooser {
                        VStack(alignment: .leading, spacing: MaktabaTokens.Spacing.s2) {
                            Text(verbatim: word("family.device.pending", pending.tierLimit.map { AgeTier.label($0) } ?? AgeTier.label(18)))
                                .fixedSize(horizontal: false, vertical: true)
                            Button { Task { await model.clear(signal) } } label: {
                                Text(verbatim: word("family.device.approve")).frame(minWidth: 88, minHeight: 44)
                            }
                            .buttonStyle(.borderedProminent)
                            .tint(Color.brass)
                            .foregroundStyle(MaktabaTokens.Colors.accentFg.color)
                            .accessibilityIdentifier("devices.approve.\(signal.id)")
                            .familyChange()
                        }
                    } else {
                        Button { Task { await model.clear(signal) } } label: {
                            Text(verbatim: word("family.device.remove")).frame(minHeight: 44)
                        }
                        .accessibilityIdentifier("devices.remove.\(signal.id)")
                        .familyChange()
                    }
                } header: {
                    // a sign-in has no name on the home: the first characters of its id tell two apart
                    Text(verbatim: word("family.device.signIn", String(signal.id.prefix(8))))
                }
            }
        }
        .maktabaSurface()
        .navigationTitle(Text(verbatim: word("family.child.devices")))
        .familyNotice(Binding { model.notice } set: { model.notice = $0 })
    }
}
