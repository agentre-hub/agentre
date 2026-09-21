// swift-tools-version: 6.0
//
// AgentreWire 是 agentre ↔ agentred wire 协议的 Swift 侧。
//
// 与 Go / TS 两侧同一条规矩:协议的主人是 pkg/wire 这个 module 里的
// proto/agentre/wire/wire.proto,产物由 `buf generate` 从那份 schema 生成并与 schema
// 同住,消费方(原生 iOS 端在另一个仓库)只钉一个已推送的不可变 revision。
//
// Sources/AgentreWire/Generated/ 整个目录是生成物:buf.gen.yaml 带 clean: true,
// 每次生成先清空它。手写的东西一律在它之外 —— 本文件与 Tests/ 就是全部手写代码。

import PackageDescription

let package = Package(
    name: "AgentreWire",
    platforms: [.iOS(.v17), .macOS(.v14)],
    products: [
        .library(name: "AgentreWire", targets: ["AgentreWire"])
    ],
    dependencies: [
        // 与 buf.gen.yaml 里钉的 buf.build/apple/swift:v1.38.1 同版本:
        // 生成器与运行时对不上时,生成的代码引用的 API 可能根本不存在。
        .package(url: "https://github.com/apple/swift-protobuf.git", exact: "1.38.1")
    ],
    targets: [
        .target(
            name: "AgentreWire",
            dependencies: [.product(name: "SwiftProtobuf", package: "swift-protobuf")]
        ),
        .testTarget(name: "AgentreWireTests", dependencies: ["AgentreWire"]),
    ]
)
