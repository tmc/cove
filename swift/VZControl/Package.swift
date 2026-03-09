// swift-tools-version: 5.9
import PackageDescription

let package = Package(
    name: "VZControl",
    platforms: [.macOS(.v14)],
    products: [
        .library(name: "VZControl", targets: ["VZControl"]),
    ],
    dependencies: [
        .package(url: "https://github.com/apple/swift-protobuf.git", from: "1.28.0"),
    ],
    targets: [
        .target(
            name: "VZControl",
            dependencies: [
                .product(name: "SwiftProtobuf", package: "swift-protobuf"),
            ]
        ),
        .testTarget(
            name: "VZControlTests",
            dependencies: ["VZControl"]
        ),
    ]
)
