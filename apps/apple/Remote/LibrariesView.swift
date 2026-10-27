import MaktabaKit
import SwiftUI

/// The home's libraries and a Scan for each: the home looks for new and changed files.
struct LibrariesView: View {
    @Environment(RemoteModel.self) private var model

    var body: some View {
        List {
            ProblemSection()
            Section {
                if model.loaded, model.problem == nil, model.libraries.isEmpty {
                    Label("No libraries yet. Add one in the home's web pages.", systemImage: "books.vertical")
                        .foregroundStyle(.secondary)
                }
                ForEach(model.libraries) { lib in
                    HStack {
                        VStack(alignment: .leading, spacing: 4) {
                            Text(lib.name).font(.headline)
                            if let roots = lib.roots, !roots.isEmpty {
                                Text(roots.joined(separator: "\n"))
                                    .font(.caption.monospaced())
                                    .foregroundStyle(.secondary)
                                    .lineLimit(3)
                            }
                        }
                        Spacer()
                        if model.scanned.contains(lib.id) {
                            Label("Scan started", systemImage: "checkmark")
                                .font(.caption)
                                .foregroundStyle(.green)
                        } else {
                            BusyButton(title: "Scan", systemImage: "arrow.triangle.2.circlepath", busy: model.busy.contains(lib.id)) {
                                await model.scan(lib)
                            }
                            .buttonStyle(.bordered)
                        }
                    }
                    .padding(.vertical, 2)
                }
            }
        }
        .maktabaSurface()
        .overlay { if !model.loaded { ProgressView() } }
        .navigationTitle("Libraries")
        .refreshable { await model.load() }
        .toolbar { AccountMenu() }
    }
}
