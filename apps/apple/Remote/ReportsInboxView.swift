import MaktabaKit
import SwiftUI

/// Problem reports (Epic 53 story 53.15 §5): the admin's inbox, as the web's
/// `/admin/reports`. A report has its text, the device, one timeline of the app's lines and
/// the home's (a request id follows that request), and the admin sets its status and writes
/// back a short reply the sender sees in My reports.
struct ReportsInboxView: View {
    @Environment(RemoteModel.self) private var model
    @State private var reports: [ReportSummary]?
    @State private var problem: String?

    var body: some View {
        List {
            if let problem {
                Section { Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red) }
            }
            if let reports, reports.isEmpty {
                Text("No problem reports.").foregroundStyle(.secondary).accessibilityIdentifier("inbox.none")
            }
            ForEach(reports ?? []) { r in
                NavigationLink {
                    ReportView(id: r.id, onChange: { await load() })
                } label: {
                    VStack(alignment: .leading, spacing: 3) {
                        HStack {
                            Text(verbatim: "#\(r.id) · \(ReportWords.category(r.category))").font(.headline)
                            Spacer()
                            Badge(text: ReportWords.status(r.status), tint: r.status == "new" ? .red : .brass)
                        }
                        Text(verbatim: [r.name, r.app].compactMap { $0 }.joined(separator: " · "))
                            .font(.caption).foregroundStyle(.secondary)
                        if let at = r.at {
                            Text(at.formatted(date: .abbreviated, time: .shortened)).font(.caption).foregroundStyle(.secondary)
                        }
                    }
                }
                .accessibilityIdentifier("inbox.report.\(r.id)")
            }
        }
        .maktabaSurface()
        .overlay { if reports == nil, problem == nil { ProgressView() } }
        .navigationTitle("Problem reports")
        .refreshable { await load() }
        .task { await load() }
        .toolbar { AccountMenu() }
    }

    private func load() async {
        do {
            reports = try await model.client.problemReports()
            problem = nil
        } catch {
            if !(error is CancellationError) { problem = HomeFailure(error).message() }
        }
    }
}

struct ReportView: View {
    let id: Int
    var onChange: () async -> Void = {}
    @Environment(RemoteModel.self) private var model
    @State private var doc: ReportDoc?
    @State private var problem: String?
    @State private var reply = ""
    @State private var busy = false

    var body: some View {
        List {
            if let problem {
                Section { Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red) }
            }
            if let d = doc {
                Section {
                    LabeledContent("From", value: d.summary.name ?? String(localized: "Unknown user"))
                    LabeledContent("Category", value: ReportWords.category(d.summary.category))
                    if let when = d.when { LabeledContent("When", value: when.formatted(date: .abbreviated, time: .shortened)) }
                    LabeledContent("App") {
                        Text(verbatim: "\(d.summary.app) \(d.version)").font(.callout.monospaced())
                    }
                    LabeledContent("Device") {
                        Text(verbatim: [d.device, d.osVersion].compactMap { $0 }.joined(separator: " · ")).font(.callout.monospaced())
                    }
                    if d.crashes > 0 { LabeledContent("Crash reports", value: d.crashes, format: .number) }
                    if d.screenshot { Label("A screenshot is attached (see it on the web)", systemImage: "photo") }
                    if let text = d.text, !text.isEmpty { Text(verbatim: text) }
                }
                Section("Status") {
                    Picker("Status", selection: Binding(get: { ReportStatus(rawValue: d.summary.status) ?? .new },
                                                        set: { s in Task { await setStatus(s) } })) {
                        ForEach(ReportStatus.allCases, id: \.self) { Text(verbatim: ReportWords.status($0)).tag($0) }
                    }
                    .disabled(busy)
                    .accessibilityIdentifier("report.status")
                    if let r = d.summary.reply, !r.isEmpty {
                        LabeledContent("Your reply", value: r)
                    }
                    TextField("Write back to the sender", text: $reply, axis: .vertical)
                        .lineLimit(2...5)
                        .accessibilityIdentifier("report.reply")
                    BusyButton(title: "Send reply", systemImage: "arrowshape.turn.up.left", busy: busy) { await sendReply() }
                        .disabled(reply.trimmingCharacters(in: .whitespaces).isEmpty)
                        .accessibilityIdentifier("report.reply.send")
                }
                Section("Timeline") {
                    if d.timeline.isEmpty { Text("No lines.").foregroundStyle(.secondary) }
                    ForEach(Array(d.timeline.enumerated()), id: \.offset) { _, row in
                        VStack(alignment: .leading, spacing: 2) {
                            Text(row.fromHome ? "Home" : "App")
                                .font(.caption2.bold())
                                .foregroundStyle(row.fromHome ? Color.blue : Color.brass)
                            if let r = row.line.requestID {
                                NavigationLink {
                                    FollowRequestView(requestID: r)
                                } label: {
                                    LogLineRow(line: row.line)
                                }
                            } else {
                                LogLineRow(line: row.line)
                            }
                        }
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay { if doc == nil, problem == nil { ProgressView() } }
        .navigationTitle(Text(verbatim: "#\(id)"))
        .task { await load() }
    }

    private func load() async {
        do {
            doc = try await model.client.problemReport(id)
            problem = nil
        } catch {
            if !(error is CancellationError) { problem = HomeFailure(error).message() }
        }
    }

    private func setStatus(_ s: ReportStatus) async {
        busy = true
        defer { busy = false }
        do { try await model.client.setReportStatus(id, s) } catch { problem = HomeFailure(error).message() }
        await load()
        await onChange()
    }

    private func sendReply() async {
        busy = true
        defer { busy = false }
        do {
            try await model.client.replyToReport(id, reply)
            reply = ""
        } catch {
            problem = HomeFailure(error).message()
        }
        await load()
    }
}
