import Foundation
import SwiftProtobuf
import XCTest

import AgentreWire

/// 与 Go 侧的二进制金向量对拍。
///
/// 向量的主人在 `pkg/wire/goldenvectors`:Go 用真实 marshaler 写出 `<name>.bin`,
/// 连同用规范 JSON 写下的期望字段值 `<name>.json`。本测试对每条向量做三件事:
///
///  1. 解 `.bin`,逐个字段断言 —— 断言写在源码里,读得出在验什么;
///  2. 解同名 `.json`,要求与 `.bin` 解出来的消息相等 —— 二进制解析器与 JSON 解析器
///     在 SwiftProtobuf 里是两套实现,同时对上才说明字段真的读对了,而不是两边犯了
///     同一个错;
///  3. 把消息再编码回去写进 `WIRE_SWIFT_ROUNDTRIP_OUT`,由驱动这一切的 Go 测试解回来,
///     要求与原消息 `proto.Equal`。
///
/// 判据**不是字节逐一相等**:protobuf 不保证跨实现的字节级一致(map 序、未知字段的
/// 摆放位置),拿字节做判据只会得到一条随实现升级而红的测试。
final class GoldenVectorTests: XCTestCase {

    // MARK: - 取材

    /// 向量目录。由本文件的位置定位,因此 `swift test` 从哪个工作目录跑都成立。
    private static let vectorsDirectory: URL = URL(fileURLWithPath: #filePath)
        .deletingLastPathComponent()  // Tests/AgentreWireTests
        .deletingLastPathComponent()  // Tests
        .deletingLastPathComponent()  // swift
        .deletingLastPathComponent()  // pkg/wire
        .appendingPathComponent("goldenvectors")
        .appendingPathComponent("vectors")

    /// Go 侧驱动时给出的回写目录。直接跑 `swift test` 时不设,只是不回写,断言照跑。
    private static let roundTripOutput: URL? = ProcessInfo.processInfo
        .environment["WIRE_SWIFT_ROUNDTRIP_OUT"]
        .map { URL(fileURLWithPath: $0) }

    /// 本次运行真正跑到的向量名。整套对拍由下面唯一那个测试方法按固定次序驱动,
    /// 所以这是一份实例状态,不是跨测试共享的全局 —— 后者在 Swift 6 的严格并发下
    /// 根本编译不过,而且 XCTest 不保证测试方法之间的次序,对账会看运气。
    private var covered: Set<String> = []

    /// 解一条向量的两种形态,断言二者相等,交给 `check` 逐字段验,再编码回去回写。
    private func roundTrip<M: SwiftProtobuf.Message & Equatable>(
        _ name: String,
        _ type: M.Type,
        check: (M) throws -> Void
    ) throws {
        covered.insert(name)

        let binaryURL = Self.vectorsDirectory.appendingPathComponent("\(name).bin")
        let jsonURL = Self.vectorsDirectory.appendingPathComponent("\(name).json")

        let fromBinary = try M(serializedBytes: try Data(contentsOf: binaryURL))
        let fromJSON = try M(jsonUTF8Data: try Data(contentsOf: jsonURL))
        XCTAssertEqual(
            fromBinary, fromJSON,
            "向量 \(name):二进制与 JSON 两条解析路径得到的消息不等")

        try check(fromBinary)

        let reencoded: Data = try fromBinary.serializedData()
        if let out = Self.roundTripOutput {
            try FileManager.default.createDirectory(
                at: out, withIntermediateDirectories: true)
            try reencoded.write(to: out.appendingPathComponent("\(name).bin"))
        }
    }

    // MARK: - 驱动

    /// 整套对拍的唯一入口。
    ///
    /// 写成一个方法而不是十几个,是为了让上面那份"跑到了哪些向量"的账目成立:XCTest
    /// 不保证测试方法之间的次序,分散成多个方法时对账那一步可能先于被对账的那些跑,
    /// 于是它要么误报、要么永远空过。
    func testGoldenVectorsMatchGoSide() throws {
        try checkSessionListRequest()
        try checkSessionListResponse()
        try checkSessionCountsResponse()
        try checkSessionAttach()
        try checkSessionPullRequest()
        try checkSessionPullResponse()
        try checkRuntimeRunRequest()
        try checkRuntimeRunRequestFresh()
        try checkRuntimeRunResponse()
        try checkRunResultDoneNotification()
        try checkRuntimeEventTextDelta()
        try checkRuntimeEventPreview()
        try checkRuntimeEventUnsetOneof()
        try checkRpcNotificationAutonomousTurnStarted()
        try checkRpcFrameRequest()
        try checkRpcFrameError()
        try checkRpcFrameUnsetBody()

        try reconcileCoverage()
    }

    // MARK: - 会话族

    private func checkSessionListRequest() throws {
        try roundTrip("session-list-request", Agentre_Wire_SessionListRequest.self) {
            XCTAssertEqual($0.keyword, "登录")
            XCTAssertEqual($0.limit, 50)
            XCTAssertEqual($0.cursor, "MTc1NDgwMDAwMDAwMA==")
            XCTAssertEqual(
                $0.conversationIds,
                [
                    "00000000-0000-7000-8000-000000000042",
                    "00000000-0000-7000-8000-000000000008",
                ])
        }
    }

    private func checkSessionListResponse() throws {
        try roundTrip("session-list-response", Agentre_Wire_SessionListResponse.self) {
            XCTAssertEqual($0.sessions.count, 2)
            XCTAssertEqual($0.cursor, "MTc1NDc5OTk5OTAwMA==")
            XCTAssertTrue($0.hasMore_p)
            XCTAssertEqual($0.total, 137)

            let rich = $0.sessions[0]
            XCTAssertEqual(rich.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual(rich.peerFingerprint, "fp-desktop")
            XCTAssertEqual(rich.agentID, 7)
            XCTAssertEqual(rich.title, "重构登录页")
            XCTAssertEqual(rich.agentSyncID, "01JZ7W2A8KZ4R5T6Y7U8I9O0P1Q")
            XCTAssertEqual(rich.providerSessionID, "sess_abc123")
            XCTAssertEqual(rich.cwd, "/home/agent/proj")
            XCTAssertEqual(rich.backendType, "claudecode")
            XCTAssertEqual(rich.lifecycleState, "running")
            XCTAssertTrue(rich.waitingForInput)
            XCTAssertEqual(rich.latestSeq, 12)
            // 毫秒时间戳超出 Double 的整数精度区间前必须仍是精确整数 ——
            // 把 int64 读成浮点的实现在这里变红。
            XCTAssertEqual(rich.lastMessageAt, 1_754_800_000_000)
            XCTAssertEqual(rich.reasoningEffort, "high")

            // 还没跑过第一轮的会话:除身份外全是默认值,而"默认值"不是"缺失"。
            let fresh = $0.sessions[1]
            XCTAssertEqual(fresh.conversationID, "00000000-0000-7000-8000-000000000008")
            XCTAssertEqual(fresh.lifecycleState, "idle")
            XCTAssertEqual(fresh.title, "")
            XCTAssertEqual(fresh.providerSessionID, "")
            XCTAssertFalse(fresh.waitingForInput)
            XCTAssertEqual(fresh.lastMessageAt, 0)
        }
    }

    private func checkSessionCountsResponse() throws {
        try roundTrip("session-counts-response", Agentre_Wire_SessionCountsResponse.self) {
            XCTAssertEqual($0.total, 137)
            XCTAssertEqual($0.running, 2)
            XCTAssertEqual($0.waiting, 3)
        }
        // 一条会话都没有是一个真实答案,不是"没答上"。全默认值的消息在线上是空字节,
        // 把"空"读成"字段缺失/不可用"的实现在这里变红。
        try roundTrip("session-counts-response-zero", Agentre_Wire_SessionCountsResponse.self) {
            XCTAssertEqual($0.total, 0)
            XCTAssertEqual($0.running, 0)
            XCTAssertEqual($0.waiting, 0)
        }
    }

    private func checkSessionAttach() throws {
        try roundTrip("session-attach-request", Agentre_Wire_SessionAttachRequest.self) {
            XCTAssertEqual($0.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual($0.peerFingerprint, "fp-desktop")
        }
        try roundTrip("session-attach-response", Agentre_Wire_SessionAttachResponse.self) {
            XCTAssertEqual($0.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual($0.backendType, "claudecode")
            XCTAssertEqual($0.lifecycleState, "running")
            XCTAssertEqual($0.latestSeq, 12)
        }
    }

    private func checkSessionPullRequest() throws {
        try roundTrip("session-pull-request", Agentre_Wire_SessionPullRequest.self) {
            XCTAssertEqual($0.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual($0.peerFingerprint, "fp-desktop")
            XCTAssertEqual($0.cursor, 0)
            XCTAssertEqual($0.limit, 200)
        }
    }

    /// 补齐页:嵌套(SessionPullResponse → DurableNotification → RpcNotification)
    /// 与 oneof 同时在一条向量上,而离线补齐正是手机侧的主路径。
    private func checkSessionPullResponse() throws {
        try roundTrip("session-pull-response", Agentre_Wire_SessionPullResponse.self) {
            XCTAssertEqual($0.notifications.count, 2)
            XCTAssertEqual($0.cursor, 12)
            XCTAssertFalse($0.hasMore_p)
            XCTAssertEqual($0.oldestSeq, 1)

            let first = $0.notifications[0]
            XCTAssertEqual(first.seq, 11)
            XCTAssertEqual(first.createtime, 1_754_800_000_000)
            guard case .runtimeEvent(let event)? = first.payload.payload else {
                return XCTFail("第一条通知应是 runtimeEvent,实得 \(String(describing: first.payload.payload))")
            }
            XCTAssertEqual(event.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual(event.seq, 11)
            guard case .textDelta(let delta)? = event.event else {
                return XCTFail("事件应是 textDelta,实得 \(String(describing: event.event))")
            }
            XCTAssertEqual(delta.text, "你好")

            let second = $0.notifications[1]
            XCTAssertEqual(second.seq, 12)
            // createtime 为 0 读作"源端没报过",不是 1970。
            XCTAssertEqual(second.createtime, 0)
            guard case .runResultDone(let done)? = second.payload.payload else {
                return XCTFail("第二条通知应是 runResultDone,实得 \(String(describing: second.payload.payload))")
            }
            XCTAssertEqual(done.usage.totalTokens, 155)
        }
    }

    // MARK: - 运行族

    private func checkRuntimeRunRequest() throws {
        try roundTrip("runtime-run-request", Agentre_Wire_RuntimeRunRequest.self) {
            XCTAssertEqual($0.backend.type, "claudecode")
            XCTAssertEqual($0.backend.id, 7)
            XCTAssertEqual($0.agentID, 7)
            XCTAssertEqual($0.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual($0.peerFingerprint, "fp-desktop")
            XCTAssertEqual($0.cwd, "/home/agent/proj")
            XCTAssertEqual($0.systemPrompt, "你是 AgentRe 的 Agent。")
            XCTAssertEqual($0.userText, "把登录按钮改成蓝色")
            XCTAssertEqual($0.permissionMode, "default")
            XCTAssertEqual($0.collaborationMode, "manual")
            XCTAssertEqual($0.reasoningEffort, "xhigh")
            XCTAssertEqual($0.sourceDevice, "fp-web-1")
            XCTAssertEqual($0.sourceDeviceName, "Chrome · macOS")

            XCTAssertEqual($0.history.count, 1)
            XCTAssertEqual($0.history[0].role, "user")
            XCTAssertEqual($0.history[0].blocks.count, 1)
            XCTAssertEqual($0.history[0].blocks[0].type, "text")
            // 转录块的 data 是不透明字节,原样透传 —— 解释它是消费方自己的事。
            XCTAssertEqual(
                String(decoding: $0.history[0].blocks[0].data, as: UTF8.self),
                "\"上一轮的上下文\"")

            XCTAssertEqual($0.mcpServers.count, 1)
            XCTAssertEqual($0.mcpServers[0].name, "org")
            XCTAssertEqual($0.mcpServers[0].url, "http://127.0.0.1:8899/mcp/org/")
            XCTAssertEqual($0.mcpServers[0].headers, ["Authorization": "Bearer tok"])
            XCTAssertEqual($0.mcpServers[0].tools, ["mcp__org__list"])
            // map 字段:值为 false 的键必须仍然在,那与"这个键不存在"是两回事。
            XCTAssertEqual($0.enabledPlugins, ["auto-continue": true, "dangerous": false])
        }
    }

    private func checkRuntimeRunRequestFresh() throws {
        try roundTrip("runtime-run-request-fresh", Agentre_Wire_RuntimeRunRequest.self) {
            XCTAssertTrue($0.freshSession)
            XCTAssertEqual($0.providerSessionID, "")
            XCTAssertEqual($0.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertTrue($0.history.isEmpty)
            XCTAssertTrue($0.mcpServers.isEmpty)
            XCTAssertTrue($0.enabledPlugins.isEmpty)
        }
    }

    private func checkRuntimeRunResponse() throws {
        try roundTrip("runtime-run-response", Agentre_Wire_RuntimeRunResponse.self) {
            XCTAssertEqual($0.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual($0.providerSessionID, "sess_abc123")
            XCTAssertEqual($0.launchPermissionMode, "default")
        }
    }

    private func checkRunResultDoneNotification() throws {
        try roundTrip("run-result-done-notification", Agentre_Wire_RunResultDoneNotification.self) {
            XCTAssertEqual($0.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual($0.seq, 12)
            XCTAssertEqual($0.providerSessionID, "sess_abc123")
            XCTAssertEqual($0.userAnchor, "anchor-1")
            XCTAssertEqual($0.model, "claude-sonnet-4-5")
            XCTAssertEqual($0.contextWindow, 200_000)
            XCTAssertEqual($0.turnToken, 9)
            XCTAssertEqual($0.usage.promptTokens, 100)
            XCTAssertEqual($0.usage.completionTokens, 50)
            XCTAssertEqual($0.usage.reasoningTokens, 10)
            XCTAssertEqual($0.usage.cachedTokens, 5)
            XCTAssertEqual($0.usage.cacheCreationTokens, 2)
            XCTAssertEqual($0.usage.totalTokens, 155)
        }
    }

    // MARK: - oneof 与嵌套

    private func checkRuntimeEventTextDelta() throws {
        try roundTrip("runtime-event-text-delta", Agentre_Wire_RuntimeEventNotification.self) {
            XCTAssertEqual($0.seq, 11)
            XCTAssertFalse($0.preview)
            guard case .textDelta(let delta)? = $0.event else {
                return XCTFail("应是 textDelta,实得 \(String(describing: $0.event))")
            }
            XCTAssertEqual(delta.text, "你好")
        }
    }

    /// 两级帧的另一级:预览帧不带 seq、带 preview。消费方据此判别,不能从 seq 是不是 0
    /// 去猜,所以两级各留一条向量。
    private func checkRuntimeEventPreview() throws {
        try roundTrip("runtime-event-preview", Agentre_Wire_RuntimeEventNotification.self) {
            XCTAssertTrue($0.preview)
            XCTAssertEqual($0.seq, 0)
            guard case .thinkingDelta(let delta)? = $0.event else {
                return XCTFail("应是 thinkingDelta,实得 \(String(describing: $0.event))")
            }
            XCTAssertEqual(delta.text, "让我想想")
        }
    }

    /// oneof **未设**。解错这条的实现会把它读成某个分支(通常是第一个),界面上表现为
    /// 凭空多出一条空文本增量 —— 跨实现最容易分歧的地方。
    private func checkRuntimeEventUnsetOneof() throws {
        try roundTrip("runtime-event-unset-oneof", Agentre_Wire_RuntimeEventNotification.self) {
            XCTAssertEqual($0.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual($0.seq, 7)
            XCTAssertNil($0.event, "未设的 oneof 必须解成 nil,不能落进任何一个分支")
        }
    }

    private func checkRpcNotificationAutonomousTurnStarted() throws {
        try roundTrip(
            "rpc-notification-autonomous-turn-started", Agentre_Wire_RpcNotification.self
        ) {
            guard case .autonomousTurnStarted(let started)? = $0.payload else {
                return XCTFail("应是 autonomousTurnStarted,实得 \(String(describing: $0.payload))")
            }
            XCTAssertEqual(started.conversationID, "00000000-0000-7000-8000-000000000042")
            XCTAssertEqual(started.seq, 13)
            XCTAssertEqual(started.trigger, "auto")
            XCTAssertEqual(started.turnToken, 9)
        }
    }

    /// 中继上真正载的那个信封:method_id + 不透明的内层字节。内层要能独立解开 ——
    /// 手机侧收到的每一条应答都要走这一步。
    private func checkRpcFrameRequest() throws {
        try roundTrip("rpc-frame-request", Agentre_Wire_RpcFrame.self) {
            XCTAssertEqual($0.id, 7)
            guard case .request(let request)? = $0.body else {
                return XCTFail("应是 request,实得 \(String(describing: $0.body))")
            }
            XCTAssertEqual(request.methodID, UInt32(Agentre_Wire_RpcMethod.sessionList.rawValue))
            let inner = try Agentre_Wire_SessionListRequest(serializedBytes: request.encodedPayload)
            XCTAssertEqual(inner.keyword, "登录")
            XCTAssertEqual(inner.limit, 50)
        }
    }

    private func checkRpcFrameError() throws {
        try roundTrip("rpc-frame-error", Agentre_Wire_RpcFrame.self) {
            XCTAssertEqual($0.id, 8)
            guard case .error(let error)? = $0.body else {
                return XCTFail("应是 error,实得 \(String(describing: $0.body))")
            }
            XCTAssertEqual(error.code, -32602)
            XCTAssertEqual(error.message, "invalid params")
            XCTAssertEqual(Array(error.details), [0x00, 0x7F, 0xFF])
        }
    }

    private func checkRpcFrameUnsetBody() throws {
        try roundTrip("rpc-frame-unset-body", Agentre_Wire_RpcFrame.self) {
            XCTAssertEqual($0.id, 9)
            XCTAssertNil($0.body, "未设的信封 body 必须解成 nil,不能落进任何一个分支")
        }
    }

    // MARK: - 对账

    /// Go 侧加了向量而 Swift 侧没跟上时变红。
    ///
    /// 没有它,新向量就只是目录里多出来的两个文件:Swift 一条断言都不会跑,而整套测试
    /// 照样全绿 —— 对拍表面成立,实际覆盖不到新协议面。
    private func reconcileCoverage() throws {
        let files = try FileManager.default.contentsOfDirectory(
            at: Self.vectorsDirectory, includingPropertiesForKeys: nil)
        let onDisk = Set(
            files.filter { $0.pathExtension == "bin" }
                .map { $0.deletingPathExtension().lastPathComponent })

        XCTAssertFalse(onDisk.isEmpty, "向量目录是空的,所有对拍都会空过")
        XCTAssertEqual(
            onDisk.subtracting(covered), [],
            "Go 侧有向量没有对应的 Swift 断言")
        XCTAssertEqual(
            covered.subtracting(onDisk), [],
            "Swift 侧引用了不存在的向量")
    }
}
