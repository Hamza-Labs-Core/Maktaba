import MaktabaKit
import SwiftUI

/// A video's deep link (FR-9): `maktaba://video/{id}` opens that video's page. The app takes
/// it (`PlayerApp`'s `onOpenURL`, the `maktaba` scheme `maktaba://connect` already uses) and the
/// shell opens the route on the section in view. The page then asks the home, so a video the
/// person may not see says "not available" (the home answers 404 for it). As Android's
/// `VideoLink` (48.28).
enum VideoLink {
    static let scheme = "maktaba"
    static let host = "video"

    /// The link for `id`.
    static func of(_ id: String) -> URL? {
        var c = URLComponents()
        c.scheme = scheme
        c.host = host
        c.path = "/" + id
        return c.url
    }

    /// The page `url` opens, or nil when it is not a video's link (a connect link, another
    /// scheme, no id, more than one path segment).
    static func route(_ url: URL) -> PlayerRoute? {
        guard url.scheme?.lowercased() == scheme,
              let c = URLComponents(url: url, resolvingAgainstBaseURL: false),
              c.host?.lowercased() == host else { return nil }
        let segments = c.percentEncodedPath.split(separator: "/", omittingEmptySubsequences: true)
        guard segments.count == 1,
              let id = String(segments[0]).removingPercentEncoding?.trimmingCharacters(in: .whitespacesAndNewlines),
              !id.isEmpty, !id.contains("/") else { return nil }
        return .video(id: id)
    }
}

/// A deep-linked page over a section that has no stack of routes (Libraries' split view, until
/// 48.18 replaces it): the page in a sheet with its own stack, and Done.
struct LinkedRouteSheet: View {
    let context: PlayerContext
    let route: PlayerRoute
    @Environment(\.dismiss) private var dismiss

    var body: some View {
        NavigationStack {
            PlayerDestination(context: context, route: route)
                .playerDestinations(context)
                .toolbar {
                    ToolbarItem(placement: .cancellationAction) {
                        Button { dismiss() } label: {
                            Text("Done", tableName: "Video", comment: "Closes a page opened from a link")
                        }
                        .accessibilityIdentifier("link.done")
                    }
                }
        }
        #if os(macOS)
        .frame(minWidth: 640, minHeight: 560)
        #endif
    }
}
