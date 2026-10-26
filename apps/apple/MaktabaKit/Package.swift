// swift-tools-version:6.0
import PackageDescription

// MaktabaKit: what every native Apple app shares (Epic 42): the API client, the session
// (tokens in the Keychain, the homes remembered), the models, and the screens every app
// opens with (choosing a home, signing in). The Player and Remote apps in
// apps/apple/Maktaba.xcodeproj depend on it; `swift test` runs its tests on any Mac.
let package = Package(
    name: "MaktabaKit",
    defaultLocalization: "en",
    platforms: [.iOS("26.0"), .macOS("15.0"), .tvOS("26.0")],
    products: [
        .library(name: "MaktabaKit", targets: ["MaktabaKit"]),
    ],
    targets: [
        .target(name: "MaktabaKit"),
        .testTarget(name: "MaktabaKitTests", dependencies: ["MaktabaKit"]),
    ]
)
