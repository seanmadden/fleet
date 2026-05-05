// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "SwiftTermSpike",
    platforms: [
        .macOS(.v13),
    ],
    dependencies: [
        .package(url: "https://github.com/migueldeicaza/SwiftTerm.git", from: "1.2.0"),
    ],
    targets: [
        .executableTarget(
            name: "SwiftTermSpike",
            dependencies: [
                .product(name: "SwiftTerm", package: "SwiftTerm"),
            ],
            path: "Sources/SwiftTermSpike"
        ),
    ]
)
