import MaktabaKit
import SwiftUI

// Epic 57 stories 57.08 and 57.15 in Family: a child's time rules (days, watching hours, a
// daily limit, a library), today's use and more time; and the child's activity by day.

private func word(_ key: String, _ args: CVarArg...) -> String {
    let format = ParentalWords.text(key)
    return args.isEmpty ? format : String(format: format, locale: AppLanguage.locale, arguments: args)
}

/// Family > a child > Time. The home counts the time and enforces the rules on every
/// device; this screen sets them and shows what the home counted.
struct TimeRulesView: View {
    let model: ChildModel
    @State private var editing: ParentalTimeRule?

    var body: some View {
        List {
            if let usage = model.usage {
                Section {
                    Text(verbatim: word("parental.time.today", ParentalWords.length(seconds: usage.seconds(on: usage.today))))
                        .accessibilityIdentifier("time.today")
                    let granted = usage.granted(on: usage.today)
                    if granted > 0 {
                        Text(verbatim: word("parental.time.granted", ParentalWords.length(seconds: granted * 60)))
                            .accessibilityIdentifier("time.granted")
                    }
                    Menu {
                        ForEach([15, 30, 60], id: \.self) { minutes in
                            Button { Task { await model.grant(minutes: minutes) } } label: {
                                Text(verbatim: ParentalWords.length(seconds: minutes * 60))
                            }
                            .accessibilityIdentifier("time.give.\(minutes)")
                        }
                    } label: {
                        Label { Text(verbatim: word("parental.time.give")) } icon: { Image(systemName: "clock.badge.checkmark") }
                            .frame(minHeight: 44)
                    }
                    .accessibilityIdentifier("time.give")
                    .familyChange()
                }
            }
            Section {
                if model.timeRules.isEmpty {
                    Text(verbatim: word("parental.time.none")).foregroundStyle(Color.mktMuted)
                        .accessibilityIdentifier("time.none")
                    Button { Task { _ = await model.saveTimeRules([ParentalTimeRule.suggested]) } } label: {
                        Text(verbatim: word("parental.time.suggest")).frame(minHeight: 44)
                    }
                    .accessibilityIdentifier("time.suggest")
                    .familyChange()
                }
                ForEach(model.timeRules) { rule in
                    Button { editing = rule } label: {
                        Text(verbatim: TimeRuleWords.summary(rule, library: rule.libraryID.flatMap { model.libraries[$0] }))
                            .frame(maxWidth: .infinity, minHeight: 44, alignment: .leading)
                    }
                    .foregroundStyle(Color.mktText)
                    .swipeActions {
                        Button(role: .destructive) {
                            Task { _ = await model.saveTimeRules(model.timeRules.filter { $0.id != rule.id }) }
                        } label: {
                            Text(verbatim: word("family.rules.remove"))
                        }
                    }
                    .accessibilityIdentifier("time.rule.\(rule.id)")
                    .familyChange()
                }
                Button { editing = ParentalTimeRule(dailyMinutes: 60) } label: {
                    Label { Text(verbatim: word("parental.time.addRule")) } icon: { Image(systemName: "plus") }
                        .frame(minHeight: 44)
                }
                .accessibilityIdentifier("time.add")
                .familyChange()
            } footer: {
                if let zone = model.timeZone { Text(verbatim: word("parental.time.zone", zone)) }
            }
        }
        .maktabaSurface()
        .navigationTitle(Text(verbatim: word("parental.time.rules")))
        .sheet(item: $editing) { rule in
            TimeRuleEditor(rule: rule, libraries: model.libraries, isNew: !model.timeRules.contains { $0.id == rule.id }) { saved in
                var rules = model.timeRules
                if let saved {
                    if let at = rules.firstIndex(where: { $0.id == saved.id }) { rules[at] = saved } else { rules.append(saved) }
                } else {
                    rules.removeAll { $0.id == rule.id }
                }
                return await model.saveTimeRules(rules)
            }
        }
        .familyNotice(Binding { model.notice } set: { model.notice = $0 })
    }
}

/// One rule: the days, the watching hours, the daily limit, the library.
private struct TimeRuleEditor: View {
    @State var rule: ParentalTimeRule
    let libraries: [String: String]
    let isNew: Bool
    /// Saves the rule (nil: removes it); true when the home took it.
    let save: (ParentalTimeRule?) async -> Bool

    @State private var hasSlot: Bool
    @State private var from: Date
    @State private var to: Date
    @State private var hasLimit: Bool
    @State private var minutes: Int
    @State private var saving = false
    @Environment(\.dismiss) private var dismiss

    init(rule: ParentalTimeRule, libraries: [String: String], isNew: Bool, save: @escaping (ParentalTimeRule?) async -> Bool) {
        _rule = State(initialValue: rule)
        self.libraries = libraries
        self.isNew = isNew
        self.save = save
        _hasSlot = State(initialValue: rule.slotStart != nil)
        _from = State(initialValue: TimeRuleWords.date(rule.slotStart ?? "07:00"))
        _to = State(initialValue: TimeRuleWords.date(rule.slotEnd ?? "20:00"))
        _hasLimit = State(initialValue: rule.dailyMinutes != nil)
        _minutes = State(initialValue: rule.dailyMinutes ?? 60)
    }

    private var valid: Bool {
        rule.days > 0 && (hasSlot || hasLimit) && (!hasSlot || TimeRuleWords.clock(from) != TimeRuleWords.clock(to))
    }

    var body: some View {
        NavigationStack {
            Form {
                Section {
                    ForEach(TimeRuleWords.days(), id: \.bit) { day in
                        Toggle(isOn: Binding { rule.covers(day: day.bit) } set: { rule.set(day: day.bit, $0) }) {
                            Text(verbatim: day.name)
                        }
                        .accessibilityIdentifier("rule.day.\(day.bit)")
                    }
                } header: {
                    Text(verbatim: word("parental.time.days"))
                }
                Section {
                    Toggle(isOn: $hasSlot) { Text(verbatim: word("parental.time.slot")) }
                        .accessibilityIdentifier("rule.slot")
                    if hasSlot {
                        DatePicker(selection: $from, displayedComponents: .hourAndMinute) { Text(verbatim: word("parental.time.from")) }
                        DatePicker(selection: $to, displayedComponents: .hourAndMinute) { Text(verbatim: word("parental.time.to")) }
                    }
                }
                Section {
                    Toggle(isOn: $hasLimit) { Text(verbatim: word("parental.time.daily")) }
                        .accessibilityIdentifier("rule.limit")
                    if hasLimit {
                        Stepper(value: $minutes, in: 15...1440, step: 15) {
                            Text(verbatim: ParentalWords.length(seconds: minutes * 60))
                        }
                        .accessibilityIdentifier("rule.minutes")
                    }
                }
                Section {
                    Picker(selection: $rule.libraryID) {
                        Text(verbatim: word("parental.time.allLibraries")).tag(String?.none)
                        ForEach(libraries.sorted { $0.value < $1.value }, id: \.key) { id, name in
                            Text(verbatim: name).tag(String?.some(id))
                        }
                    } label: {
                        Text(verbatim: word("parental.time.library"))
                    }
                } footer: {
                    if !valid { Text(verbatim: word("parental.time.invalid")) }
                }
                if !isNew {
                    Section {
                        Button(role: .destructive) {
                            Task { if await save(nil) { dismiss() } }
                        } label: {
                            Text(verbatim: word("family.rules.remove")).frame(maxWidth: .infinity, minHeight: 44)
                        }
                        .accessibilityIdentifier("rule.remove")
                    }
                }
            }
            .maktabaSurface()
            .navigationTitle(Text(verbatim: word("parental.time.rules")))
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button { dismiss() } label: { Text(verbatim: word("parental.pin.cancel")) }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button {
                        Task {
                            saving = true
                            defer { saving = false }
                            var out = rule
                            out.slotStart = hasSlot ? TimeRuleWords.clock(from) : nil
                            out.slotEnd = hasSlot ? TimeRuleWords.clock(to) : nil
                            out.dailyMinutes = hasLimit ? minutes : nil
                            if await save(out) { dismiss() }
                        }
                    } label: {
                        Text(verbatim: word("family.pin.save"))
                    }
                    .disabled(!valid || saving)
                    .accessibilityIdentifier("rule.save")
                }
            }
        }
    }
}

/// Family > a child > Activity: what was watched, blocked and asked for, by day, with one
/// filter; and the time watched each day. Kept on the home for 90 days.
struct ActivityView: View {
    let usage: ParentalTimeUsage?
    let libraries: [String: String]
    @State private var model: ActivityModel

    init(client: APIClient, child: ParentalPerson, usage: ParentalTimeUsage?, libraries: [String: String]) {
        self.usage = usage
        self.libraries = libraries
        _model = State(initialValue: ActivityModel(client: client, child: child))
    }

    /// The app's calendar in the home's time zone (days follow the home's clock).
    private var calendar: Calendar {
        var c = Calendar.current
        c.locale = AppLanguage.locale
        if let zone = usage?.timeZone.flatMap(TimeZone.init(identifier:)) { c.timeZone = zone }
        return c
    }

    var body: some View {
        List {
            Section {
                Picker(selection: Binding { model.filter } set: { f in Task { await model.show(f) } }) {
                    ForEach(ActivityModel.Filter.allCases, id: \.self) { Text(verbatim: word($0.key)).tag($0) }
                } label: {
                    Text(verbatim: word("activity.title"))
                }
                .pickerStyle(.segmented)
                .accessibilityIdentifier("activity.filter")
            }
            if model.filter == .all, let usage, !usage.usage.isEmpty {
                Section {
                    ForEach(Array(Set(usage.usage.map(\.day))).sorted(by: >), id: \.self) { day in
                        LabeledContent {
                            Text(verbatim: ParentalWords.length(seconds: usage.seconds(on: day)))
                        } label: {
                            Text(verbatim: day == usage.today ? word("activity.day.today") : "\u{200E}\(day)")
                        }
                    }
                } header: {
                    Text(verbatim: word("activity.time.title")).accessibilityAddTraits(.isHeader)
                }
            }
            switch model.events {
            case .loading:
                ProgressView().frame(maxWidth: .infinity)
            case let .failed(failure):
                Text(verbatim: failure.message(video: false)).foregroundStyle(Color.mktMuted)
            case .ready:
                let days = model.days(calendar: calendar)
                if days.isEmpty {
                    Text(verbatim: word("activity.none")).foregroundStyle(Color.mktMuted)
                        .accessibilityIdentifier("activity.none")
                }
                ForEach(days) { day in
                    Section {
                        ForEach(day.events) { event in
                            Label {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(verbatim: ActivityModel.line(event)).fixedSize(horizontal: false, vertical: true)
                                    Text(verbatim: ParentalWords.clock(event.at, zone: calendar.timeZone))
                                        .font(.footnote)
                                        .foregroundStyle(Color.mktMuted)
                                }
                            } icon: {
                                Image(systemName: ActivityModel.symbol(event.kind))
                            }
                            .accessibilityElement(children: .combine)
                            .accessibilityIdentifier("activity.event.\(event.id)")
                        }
                    } header: {
                        Text(verbatim: ActivityModel.heading(day.id, calendar: calendar)).accessibilityAddTraits(.isHeader)
                    }
                }
                if model.next != nil {
                    Button { Task { await model.more() } } label: {
                        Text(verbatim: word("activity.more")).frame(maxWidth: .infinity, minHeight: 44)
                    }
                    .accessibilityIdentifier("activity.more")
                }
            }
            Section {} footer: {
                Text(verbatim: word("activity.kept")).accessibilityIdentifier("activity.kept")
            }
        }
        .maktabaSurface()
        .navigationTitle(Text(verbatim: word("activity.title")))
        .task { await model.load() }
        .refreshable { await model.load() }
        .accessibilityIdentifier("screen.activity")
    }
}
