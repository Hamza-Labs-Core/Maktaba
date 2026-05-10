// swift-tools-version:5.9
import PackageDescription

let package = Package(
    name: "Maktaba",
    platforms: [.tvOS(.v17)],
    products: [
        .library(name: "Maktaba", targets: ["Maktaba"])
    ],
    dependencies: [
        .package(url: "https://github.com/apollographql/apollo-ios", from: "1.9.0")
    ],
    targets: [
        .target(
            name: "Maktaba",
            dependencies: [
                .product(name: "Apollo", package: "apollo-ios")
            ],
            path: "Sources/Maktaba"
        ),
        .testTarget(
            name: "MaktabaTests",
            dependencies: ["Maktaba"],
            path: "Tests/MaktabaTests"
        )
    ]
)
