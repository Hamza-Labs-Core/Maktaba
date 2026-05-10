import Foundation

// Generated stubs that stand in for Apollo iOS codegen output.
// The real codegen reads `shared/graphql/schema.graphql` and emits
// strongly-typed query/mutation/subscription structs. Until codegen is
// wired into the Xcode project, this file documents the contract.

public enum MaktabaSchema {
    public static let endpoint = "/graphql"

    public struct ContinueWatchingQuery {
        public static let body = """
        query ContinueWatching($limit: Int = 12) {
          continueWatching(limit: $limit) {
            id
            title
            durationSec
            positionSec
            posterUrl
          }
        }
        """
    }

    public struct RecommendationsQuery {
        public static let body = """
        query Recommendations($limit: Int = 12) {
          recommendations(limit: $limit) {
            id
            title
            durationSec
            reason
            posterUrl
          }
        }
        """
    }

    public struct SearchQuery {
        public static let body = """
        query Search($q: String!, $limit: Int = 24) {
          search(q: $q, limit: $limit) {
            id
            title
            durationSec
            snippet
            posterUrl
          }
        }
        """
    }
}
