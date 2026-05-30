// swift-tools-version:5.9
import PackageDescription

// Apollo iOS codegen is the eventual client (Story 14.1 AC) but it is
// gated on the Xcode/tvOS build pipeline, which is not present in this
// repo's CI. Until that lands, the GraphQL operations are executed by
// a hand-rolled URLSession client (Services/PairingService.swift,
// GraphQLClient) against the live /graphql backbone — real network
// calls, real decoding, fully unit-tested host-side. Apollo is left
// out of the dependency graph so the core compiles and tests without
// the tvOS SDK; re-add it when the Xcode codegen step is wired.
let package = Package(
    name: "Maktaba",
    // tvOS is the ship target; macOS 12 is declared only so the
    // non-UI core (networking / models) compiles and unit-tests on a
    // CI runner without the tvOS SDK. SwiftUI screens are guarded by
    // #if canImport(SwiftUI) so the package still builds host-side.
    platforms: [.tvOS(.v17), .macOS(.v12)],
    products: [
        .library(name: "Maktaba", targets: ["Maktaba"])
    ],
    targets: [
        .target(
            name: "Maktaba",
            path: "Sources/Maktaba"
        ),
        .testTarget(
            name: "MaktabaTests",
            dependencies: ["Maktaba"],
            path: "Tests/MaktabaTests"
        )
    ]
)
