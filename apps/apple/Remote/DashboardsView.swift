import Charts
import MaktabaKit
import SwiftUI

/// Dashboards on the Remote (Epic 53 story 53.12 §4): Overview and Storage, read only, from
/// the same call as the web's (`GET /api/admin/dashboards/{board}?range=`). One chart per
/// metric, a line per label set; time runs left to right in Arabic too (as Arabic charts
/// do), and each chart says its latest and highest value to VoiceOver. With several machines
/// the Overview shows totals across them and the five most in need of attention, says how
/// many of how many it shows, names the joined machines that are offline (they come first),
/// and offers each machine on its own.
struct DashboardsView: View {
    @Environment(RemoteModel.self) private var model
    @State private var board = Board.overview
    @State private var range = BoardRange.day
    @State private var machine = ""
    @State private var machines: [String] = []
    @State private var offline: [String] = []
    @State private var doc: BoardDoc?
    @State private var problem: String?

    var body: some View {
        List {
            Section {
                Picker("Board", selection: $board) {
                    Text("Overview").tag(Board.overview)
                    Text("Storage").tag(Board.storage)
                }
                .pickerStyle(.segmented)
                .accessibilityIdentifier("dash.board")
                Picker("Time range", selection: $range) {
                    Text("1 hour").tag(BoardRange.hour)
                    Text("24 hours").tag(BoardRange.day)
                    Text("7 days").tag(BoardRange.week)
                    Text("30 days").tag(BoardRange.month)
                    Text("90 days").tag(BoardRange.quarter)
                }
                .accessibilityIdentifier("dash.range")
                if board == .overview, machines.count > 1 {
                    Picker("Machine", selection: $machine) {
                        Text("All machines").tag("")
                        ForEach(machines, id: \.self) { m in
                            if offline.contains(m) {
                                Text("\(m) (offline)").tag(m)
                            } else {
                                Text(verbatim: m).tag(m)
                            }
                        }
                    }
                    .accessibilityIdentifier("dash.machine")
                }
            }
            if let problem {
                Section { Label(problem, systemImage: "exclamationmark.triangle").foregroundStyle(.red) }
            }
            if let doc, board == .overview, doc.showsSomeMachines, let m = doc.machines {
                Text("Showing \(m.shown.count) of \(m.total) machines, those most in need of attention first. Pick a machine to see its own.")
                    .font(.footnote).foregroundStyle(.secondary)
                    .accessibilityIdentifier("dash.machines.shown")
            }
            if let doc, board == .overview, let m = doc.machines, !m.offline.isEmpty {
                if let one = doc.machine {
                    if m.isOffline(one) {
                        Label("This machine is offline. Its lines end at its last values.", systemImage: "bolt.horizontal.circle")
                            .font(.footnote).foregroundStyle(.red)
                            .accessibilityIdentifier("dash.offline")
                    }
                } else {
                    Label("Offline: \(m.offline.joined(separator: ", ")). Offline machines come first; their lines end at their last values.",
                          systemImage: "bolt.horizontal.circle")
                        .font(.footnote).foregroundStyle(.red)
                        .accessibilityIdentifier("dash.offline")
                }
            }
            if let doc, doc.omitted > 0 {
                Text("\(doc.omitted) more lines did not fit on this board.")
                    .font(.footnote).foregroundStyle(.secondary)
                    .accessibilityIdentifier("dash.omitted")
            }
            if let doc {
                if doc.panels.allSatisfy({ $0.series.allSatisfy(\.points.isEmpty) }) {
                    Text("No data for this time yet").foregroundStyle(.secondary).accessibilityIdentifier("dash.noData")
                }
                ForEach(doc.panels, id: \.name) { panel in
                    if panel.series.contains(where: { !$0.points.isEmpty }) {
                        Section {
                            PanelChart(name: panel.name, series: panel.series)
                        } header: {
                            Text(verbatim: DashWords.title(panel.name))
                        }
                    }
                }
            }
        }
        .maktabaSurface()
        .overlay { if doc == nil, problem == nil { ProgressView() } }
        .navigationTitle("Dashboards")
        .refreshable { await load() }
        .task(id: "\(board.rawValue)|\(range.rawValue)|\(machine)") { await load() }
        .toolbar { AccountMenu() }
    }

    private func load() async {
        do {
            let d = try await model.client.board(board, range: range, machine: machine)
            doc = d
            if let m = d.machines {
                machines = m.all
                offline = m.offline
            }
            problem = nil
        } catch {
            if !(error is CancellationError) { problem = HomeFailure(error).message() }
        }
    }
}

struct PanelChart: View {
    let name: String
    let series: [BoardDoc.Series]

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Chart {
                ForEach(series, id: \.self) { s in
                    ForEach(s.points, id: \.at) { p in
                        LineMark(x: .value("Time", p.at), y: .value("Value", p.value))
                            .foregroundStyle(by: .value("Series", s.legend.isEmpty ? DashWords.title(name) : s.legend))
                    }
                }
            }
            .chartYAxis {
                AxisMarks { v in
                    AxisGridLine()
                    AxisValueLabel { if let d = v.as(Double.self) { Text(verbatim: DashWords.value(d, of: name)) } }
                }
            }
            .chartLegend(series.count > 1 ? .visible : .hidden)
            .frame(height: 160)
            .environment(\.layoutDirection, .leftToRight)
            .accessibilityElement()
            .accessibilityLabel(Text(verbatim: summary))
            .accessibilityIdentifier("dash.panel.\(name)")
        }
    }

    /// "Log files: now 2 MB; highest 3 MB."
    var summary: String {
        // the series summed at each time: "now" is the last time's, "highest" the largest
        var byTime: [Date: Double] = [:]
        for s in series { for p in s.points { byTime[p.at, default: 0] += p.value } }
        let times = byTime.keys.sorted()
        let latest = times.last.flatMap { byTime[$0] } ?? 0
        let max = byTime.values.max() ?? 0
        return String(localized: "\(DashWords.title(name)): now \(DashWords.value(latest, of: name)); highest \(DashWords.value(max, of: name)).")
    }
}

enum DashWords {
    /// A panel's title: the web's words for the metric, else its name.
    static func title(_ name: String) -> String {
        switch name {
        case "maktaba_component_up": String(localized: "Components up")
        case "maktaba_component_down": String(localized: "Components down")
        case "maktaba_machines_components_up": String(localized: "Components up, all machines")
        case "maktaba_machines_components_down": String(localized: "Components down, all machines")
        case "maktaba_machines_disk_free_min_bytes": String(localized: "Least disk free of any machine")
        case "maktaba_machines_memory_free_min_bytes": String(localized: "Least memory free of any machine")
        case "maktaba_machines_load1_max": String(localized: "Highest load average (1 min) of any machine")
        case "maktaba_machines_restarts": String(localized: "Restarts per minute, all machines")
        case "maktaba_component_restarts_total": String(localized: "Restarts per minute")
        case "maktaba_errors_per_hour": String(localized: "Errors per hour")
        case "process_resident_memory_bytes": String(localized: "Memory of the API")
        case "process_cpu_seconds_total": String(localized: "CPU of the API (seconds per minute)")
        case "go_goroutines": String(localized: "API goroutines")
        case "maktaba_host_load1": String(localized: "Load average (1 min)")
        case "maktaba_host_cpus": String(localized: "CPUs")
        case "maktaba_host_memory_bytes": String(localized: "Memory")
        case "maktaba_host_memory_free_bytes": String(localized: "Memory free")
        case "maktaba_host_disk_free_bytes": String(localized: "Disk free (data folder)")
        case "maktaba_supervisor_children": String(localized: "Processes running")
        case "http_request_duration_seconds_count": String(localized: "API requests per minute")
        case "http_request_duration_seconds_p95": String(localized: "API response time, p95 (seconds)")
        case "maktaba_update_machines_behind": String(localized: "Machines behind on updates")
        case "maktaba_tv_channels_on_air": String(localized: "TV channels on air")
        case "maktaba_tv_frontdoor_bytes_total": String(localized: "TV bytes out per minute")
        case "maktaba_tv_remote_viewers": String(localized: "Viewers away from home")
        case "maktaba_tv_failovers_total": String(localized: "TV failovers per minute")
        case "maktaba_update_backup_bytes": String(localized: "Update backups")
        case "maktaba_logs_segments_bytes": String(localized: "Log files")
        case "maktaba_logs_index_bytes": String(localized: "Log search index")
        default: name
        }
    }

    /// Bytes as sizes, the rest as numbers, in the app's language.
    static func value(_ v: Double, of name: String) -> String {
        if name.contains("bytes") {
            return Int64(v).formatted(.byteCount(style: .file))
        }
        return v.formatted(.number.precision(.fractionLength(0...1)))
    }
}
