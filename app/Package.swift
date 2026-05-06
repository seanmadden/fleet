// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "Fleet",
    platforms: [
        .macOS(.v14),
    ],
    dependencies: [
        .package(url: "https://github.com/migueldeicaza/SwiftTerm.git", from: "1.2.0"),
    ],
    targets: [
        .executableTarget(
            name: "Fleet",
            dependencies: [
                .product(name: "SwiftTerm", package: "SwiftTerm"),
            ],
            path: "Sources/Fleet",
            resources: [
                .process("Resources"),
            ]
        ),
    ]
)
