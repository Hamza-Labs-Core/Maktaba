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
        // Resources/: the brand every app shares (Epic 48 FR-4): Amiri Bold and IBM Plex
        // Sans Arabic (Fonts/, with their OFL licences), the web's library art and the logo
        // (Art/). `.process` flattens them into Bundle.module; Brand.swift loads them.
        .target(name: "MaktabaKit", resources: [.process("Localizable.xcstrings"), .process("Resources")]),
        .testTarget(name: "MaktabaKitTests", dependencies: ["MaktabaKit"]),
    ]
)
