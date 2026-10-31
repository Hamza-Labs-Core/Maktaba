import MaktabaKit
import SwiftUI

/// The home's updates, read only (the web's Admin -> Updates): what runs and what is next,
/// what holds a release, the releases that are blocked, a TV held at an update, the run
/// under way (or the last one) with its steps and machines, and every machine's release.
struct UpdatesView: View {
    @Environment(RemoteModel.self) private var model

    var body: some View {
        List {
            if let problem = model.statusProblems[.updates] {
                Section {
                    Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red)
                }
            }
            if let o = model.updates {
                alerts(o)
                homeSection(o)
                if let run = o.shownRun {
                    RunSections(run: run, view: model.updateRun?.run.id == run.id ? model.updateRun : nil,
                                active: o.activeRun != nil, problem: model.statusProblems[.updateRun])
                }
                if !o.machines.isEmpty {
                    Section("Machines") {
                        ForEach(o.machines) { UpdateMachineRow(machine: $0) }
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay {
            if model.updates == nil {
                if model.statusProblems[.updates] == nil { ProgressView() }
            }
        }
        .navigationTitle("Updates")
        .refreshable { await model.load() }
        .toolbar { AccountMenu() }
    }

    @ViewBuilder private func alerts(_ o: UpdatesOverview) -> some View {
        if o.checkStale || o.stuck != nil || o.tvHeld != nil || !o.blockedReleases.isEmpty {
            Section {
                if o.checkStale {
                    Callout(systemImage: "wifi.exclamationmark", tint: .red,
                            title: "No successful update check for 14 days. Check the network or GitHub access.",
                            detail: o.lastCheckError)
                }
                if let stuck = o.stuck, o.activeRun == nil {
                    Callout(systemImage: "clock.badge.exclamationmark", tint: .brass, title: stuck.sentence, detail: nil)
                }
                if let held = o.tvHeld {
                    TVCallout(tv: held.tv, release: held.version)
                }
                ForEach(o.blockedReleases) { r in
                    Callout(systemImage: "nosign", tint: .red,
                            title: "\(r.version) is blocked: \(UpdateWords.blocked(r.blockedReason ?? ""))",
                            detail: nil)
                        .accessibilityIdentifier("updates.blocked.\(r.version)")
                }
            }
        } else if o.lastCheckError.map({ !$0.isEmpty }) == true {
            Section {
                Callout(systemImage: "exclamationmark.icloud", tint: .secondary, title: "The last update check did not work.",
                        detail: o.lastCheckError)
            }
        }
    }

    @ViewBuilder private func homeSection(_ o: UpdatesOverview) -> some View {
        Section("This home") {
            let home = o.machines.first { $0.role == "home" }
            LabeledContent("Running", value: (home?.version).flatMap { $0.isEmpty ? nil : $0 } ?? o.currentVersion)
            VStack(alignment: .leading, spacing: 4) {
                Text("Next").font(.subheadline).foregroundStyle(.secondary)
                Text(o.next())
                    .accessibilityIdentifier("updates.next")
            }
            LabeledContent("Automatic updates", value: autoText(o))
            if let target = o.plan.target, o.activeRun == nil {
                LabeledContent("Target", value: target.version)
                if o.plan.steppingStone {
                    Label(steppingStone(o, target: target), systemImage: "stairs")
                        .font(.callout)
                        .foregroundStyle(.secondary)
                }
                if o.plan.held {
                    Label("Held: it waits for your approval.", systemImage: "hand.raised")
                        .foregroundStyle(Color.brass)
                }
                if let at = o.plan.eligibleAt, at > Date() {
                    LabeledContent("Old enough on", value: at.formatted(date: .abbreviated, time: .shortened))
                }
            } else if let reason = o.plan.reason, reason != "up_to_date", !reason.isEmpty {
                Text(UpdateWords.planReason(reason)).foregroundStyle(.secondary)
            }
            if let ok = o.lastCheckOK {
                LabeledContent("Last checked", value: ok.formatted(.relative(presentation: .named)))
            }
        }
    }

    private func autoText(_ o: UpdatesOverview) -> String {
        guard o.autoInstall else { return "Paused" }
        let channel = o.channel.map { "\($0) channel" }
        if let (from, to) = UpdateWords.windowEnds(o.window) {
            return ["On", channel, "quiet window \(from)–\(to)"].compactMap { $0 }.joined(separator: " · ")
        }
        return ["On", channel].compactMap { $0 }.joined(separator: " · ")
    }

    private func steppingStone(_ o: UpdatesOverview, target: CatalogRelease) -> String {
        let newest = o.plan.newest?.version ?? "the newest release"
        return "A stepping stone: \(newest) cannot be installed from this version, so \(target.version) comes first and the next window goes on from there."
    }
}

/// A run: its state, step and error, a TV held, its steps and its machines.
private struct RunSections: View {
    let run: UpdateRun
    let view: UpdateRunView?
    let active: Bool
    let problem: String?

    var body: some View {
        Section(active ? "Update under way" : "Last update") {
            HStack {
                Text(run.version.isEmpty ? "Update" : "To \(run.version)").font(.headline)
                if let from = run.fromVersion, !from.isEmpty {
                    Text("from \(from)").foregroundStyle(.secondary)
                }
                Spacer()
                Badge(text: UpdateWords.state(run.state), tint: runTint(run.state))
            }
            if let step = run.step, !step.isEmpty, !run.ended {
                LabeledContent("Step", value: UpdateWords.step(step))
            }
            if let waiting = view?.waiting {
                Label(UpdateWords.waiting(waiting), systemImage: "hourglass").foregroundStyle(.secondary)
            }
            if let started = run.startedAt {
                LabeledContent("Started", value: started.formatted(date: .abbreviated, time: .shortened))
            }
            if let finished = run.finishedAt {
                LabeledContent("Ended", value: finished.formatted(date: .abbreviated, time: .shortened))
            }
            if let error = run.errorText {
                VStack(alignment: .leading, spacing: 4) {
                    Label(error, systemImage: "xmark.octagon").foregroundStyle(.red)
                    if let detail = run.error["detail"], !detail.isEmpty {
                        Text(detail).font(.caption).foregroundStyle(.secondary)
                    }
                }
            }
            if let tv = view?.tv {
                TVCallout(tv: tv, release: run.version)
            }
            if let problem {
                Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red)
            }
        }
        if let view, !view.steps.isEmpty {
            Section("Steps") {
                ForEach(view.steps) { s in
                    HStack(alignment: .firstTextBaseline) {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(UpdateWords.step(s.step))
                            Text([s.host, s.detail].compactMap { $0?.isEmpty == false ? $0 : nil }.joined(separator: " · "))
                                .font(.caption)
                                .foregroundStyle(.secondary)
                        }
                        Spacer()
                        Badge(text: UpdateWords.state(s.state), tint: stepTint(s.state))
                    }
                }
            }
        }
        if let view, !view.targets.isEmpty {
            Section("Machines in this update") {
                ForEach(view.targets) { t in
                    VStack(alignment: .leading, spacing: 4) {
                        HStack(alignment: .firstTextBaseline) {
                            Text(t.name.isEmpty ? t.nodeID : t.name).font(.headline)
                            Spacer()
                            Badge(text: UpdateWords.state(t.state), tint: targetTint(t.state))
                        }
                        Text([t.platform, versions(t)].compactMap { $0 }.joined(separator: " · "))
                            .font(.caption)
                            .foregroundStyle(.secondary)
                        if let error = t.errorText {
                            Text(error).font(.callout).foregroundStyle(.red)
                        }
                        if let detail = t.error["detail"], !detail.isEmpty {
                            Text(detail).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
            }
        } else if let view, !view.waitingHome.isEmpty {
            Section {
                ForEach(view.waitingHome) { m in
                    LabeledContent(m.name, value: "Waiting for the home to finish")
                }
            } header: {
                Text("Machines")
            } footer: {
                Text("The machines update once the home has committed \(run.version): they wait for the home to finish.")
            }
        }
    }

    private func versions(_ t: UpdateTarget) -> String? {
        switch (t.fromVersion, t.toVersion) {
        case let (from?, to?) where !from.isEmpty && !to.isEmpty: "\(from) → \(to)"
        case let (_, to?) where !to.isEmpty: "to \(to)"
        default: nil
        }
    }

    private func runTint(_ s: String) -> Color {
        switch s {
        case "done": .green
        case "failed", "rolled_back", "failed_rollback": .red
        case "done_with_failures", "done_needs_person", "held": .brass
        case "cancelled", "superseded": .secondary
        default: .blue
        }
    }

    private func stepTint(_ s: String) -> Color {
        switch s {
        case "done", "ok", "passed": .green
        case "failed": .red
        case "skipped": .secondary
        default: .blue
        }
    }

    private func targetTint(_ s: String) -> Color {
        switch s {
        case "healthy", "committed": .green
        case "failed", "failed_prepare", "rolled_back", "apply_failed", "prepare_failed": .red
        case "held", "needs_person", "offline": .brass
        case "skipped", "worker_only", "pending": .secondary
        default: .blue
        }
    }
}

/// A machine's release on the Updates page.
private struct UpdateMachineRow: View {
    let machine: UpdateMachine

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .firstTextBaseline) {
                Image(systemName: machine.role == "home" ? "house" : "desktopcomputer")
                    .foregroundStyle(.secondary)
                Text(machine.name).font(.headline)
                Spacer()
                Badge(text: UpdateWords.state(machine.state), tint: tint)
            }
            Text(facts).font(.caption).foregroundStyle(.secondary)
            if let last = machine.lastUpdate, !last.to.isEmpty {
                Text("Last update: \(last.to), \((last.endedAt ?? last.at)?.formatted(.relative(presentation: .named)) ?? "") (\(last.how))")
                    .font(.caption)
                    .foregroundStyle(.secondary)
            }
            if let db = machine.homeDB, !db.ok {
                Label(db.sentence, systemImage: "cylinder.split.1x2")
                    .font(.callout)
                    .foregroundStyle(db.healing ? Color.brass : .red)
                if let detail = db.detail, !detail.isEmpty {
                    Text(detail).font(.caption).foregroundStyle(.secondary)
                }
            }
        }
    }

    private var facts: String {
        var parts = [machine.version.isEmpty ? "version unknown" : machine.version]
        if let target = machine.target, !target.isEmpty, target != machine.version { parts.append("updating to \(target)") }
        if let p = machine.platform, !p.isEmpty { parts.append(p) }
        return parts.joined(separator: " · ")
    }

    private var tint: Color {
        switch machine.state {
        case "up_to_date", "healthy", "committed": .green
        case "behind", "held", "needs_person": .brass
        case "offline", "failed", "rolled_back": .red
        case "worker_only", "ahead_of_home": .secondary
        default: .blue
        }
    }
}

/// A TV held at an update (or what became of it): its title, what it means, the failed checks.
private struct TVCallout: View {
    let tv: RunTV
    let release: String

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Label {
                Text(tv.title(release: release)).font(.headline)
            } icon: {
                Image(systemName: "tv").foregroundStyle(tv.held ? Color.brass : .secondary)
            }
            if let n = tv.number {
                Text("TV change #\(n)").font(.caption).foregroundStyle(.secondary)
            }
            if let body = tv.heldBody {
                Text(body).font(.callout).foregroundStyle(.secondary)
            }
            if tv.held {
                ForEach(tv.failedChecks, id: \.self) { c in
                    Label(c.sentence, systemImage: "xmark.circle")
                        .font(.callout)
                        .foregroundStyle(.red)
                }
            }
        }
        .accessibilityIdentifier("updates.tv")
    }
}

/// A line that wants attention: an icon, a sentence, and the home's own words below.
struct Callout: View {
    let systemImage: String
    let tint: Color
    let title: String
    let detail: String?

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Label {
                Text(title)
            } icon: {
                Image(systemName: systemImage).foregroundStyle(tint)
            }
            if let detail, !detail.isEmpty {
                Text(detail).font(.caption).foregroundStyle(.secondary)
            }
        }
    }
}

/// A state in a coloured capsule.
struct Badge: View {
    let text: String
    let tint: Color

    var body: some View {
        Text(text)
            .font(.caption.bold())
            .lineLimit(1)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(tint.opacity(0.18), in: .capsule)
            .foregroundStyle(tint)
    }
}
