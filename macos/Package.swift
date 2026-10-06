// swift-tools-version:6.0
import PackageDescription

let package = Package(
    name: "Tackroom",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(name: "Tackroom", path: "Sources/Tackroom"),
        .testTarget(name: "TackroomTests", dependencies: ["Tackroom"], path: "Tests/TackroomTests")
    ],
    // Swift 5 mode: Process, WKWebView and URLSession glue without Sendable ceremony.
    swiftLanguageModes: [.v5]
)
