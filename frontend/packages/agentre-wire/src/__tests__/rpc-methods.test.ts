import { describe, expect, it } from "vitest";

import {
  ProtobufRpcCodec,
  rpcMethods,
  encodeRpcMethodRequest,
  decodeRpcMethodResponse,
} from "../index";
import {
  AgentredSelfUpdateRejectReason,
  RpcMethod,
} from "../gen/agentre/wire/wire_pb";

// 线上对话身份是 uuid;这些用例要证的是"同一个值原样往返",取一个可读的固定值。
const CONVERSATION_ID = "00000000-0000-7000-8000-000000000042";

describe("typed protobuf RPC methods", () => {
  it("registers every stable production method ID exactly once", () => {
    // ID 是 proto 枚举里的全局稳定值,不是本表的下标:52–56(转录导入四条 + 活动
    // 汇总)至今只有桌面↔daemon 在走,浏览器侧没有调用方,所以这里是一段连号加上
    // 会话思考力度那条(57)与 agentred 自更新那条(58)。
    expect(
      Object.values(rpcMethods)
        .map((method) => method.id)
        .sort((a, b) => a - b),
    ).toEqual([
      ...Array.from({ length: 51 }, (_, index) => index + 1),
      57,
      58,
      59,
    ]);
  });
  // 这张表是手写的:id 写错不会被编译器发现,只会在对端解出「未知 method ID」时爆掉。
  // 新加的这条因此与生成的枚举直接对钉。
  it("pairs the session counts descriptor with its generated proto ID", () => {
    expect(rpcMethods.sessionCounts.id).toBe(RpcMethod.SESSION_COUNTS);
  });

  it("pairs the session reasoning effort descriptor with its generated proto ID", () => {
    expect(rpcMethods.setSessionReasoningEffort.id).toBe(
      RpcMethod.SET_SESSION_REASONING_EFFORT,
    );
  });

  // agentred 自更新(spec 2026-09-03):浏览器与桌面端都要能发起它,所以它必须落在
  // 本表里而不只是 Go 侧的方法常量。
  it("pairs the agentred self-update descriptor with its generated proto ID", () => {
    expect(rpcMethods.agentredSelfUpdate.id).toBe(
      RpcMethod.AGENTRED_SELF_UPDATE,
    );
  });

  it("encodes runtime.run by stable method ID without exposing payload bytes", () => {
    const payload = encodeRpcMethodRequest(9n, rpcMethods.runtimeRun, {
      conversationId: CONVERSATION_ID,
      userText: "hello",
    });
    expect(ProtobufRpcCodec.decode(payload)).toEqual({
      id: 9n,
      body: {
        case: "typedMethodRequest",
        methodId: 17,
        method: "runtimeRun",
        value: expect.objectContaining({
          conversationId: CONVERSATION_ID,
          userText: "hello",
        }),
      },
    });
  });

  it("round-trips server production method families through typed descriptors", () => {
    const cases = [
      [rpcMethods.sessionPendingWaiters, { conversationId: CONVERSATION_ID }],
      [
        rpcMethods.setModelTarget,
        { conversationId: CONVERSATION_ID, providerKey: "p" },
      ],
      [rpcMethods.runtimeCapabilities, { backendType: "claudecode" }],
      // 会话思考力度:空串是**要写下去的值**(改回跟随后端配置),所以它必须能被
      // 独立编码进请求,而不是靠「省略即不改」。
      [
        rpcMethods.setSessionReasoningEffort,
        { conversationId: CONVERSATION_ID, reasoningEffort: "xhigh" },
      ],
      // 自更新请求带通道与「越过活跃轮次」标志(spec 2026-09-03 决策 8):force 必须
      // 是请求里的显式一位,而不是靠调用方重试来表达。
      [rpcMethods.agentredSelfUpdate, { channel: "stable", force: true }],
      [rpcMethods.skillCatalog, { backendType: "claudecode" }],
      [rpcMethods.projectSetLocalPath, { projectSyncId: "p", path: "/tmp" }],
      [rpcMethods.remoteFsListDir, { path: "/tmp" }],
      [rpcMethods.workspaceFsReadFile, { root: "/tmp", relPath: "a.txt" }],
      [rpcMethods.engineDiscover, { providerKey: "p" }],
    ] as const;
    for (const [method, value] of cases) {
      const encoded = encodeRpcMethodRequest(1n, method, value);
      const decoded = ProtobufRpcCodec.decode(encoded);
      expect(decoded.body).toMatchObject({
        case: "typedMethodRequest",
        methodId: method.id,
        method: method.name,
      });
    }
  });

  // 应答只回受理结果、不回升级进度(spec「远程一键升级」),但拒绝原因必须逐条可
  // 判别:界面按原因分支(活跃轮次那条还要显示条数并走二次确认),只回一句人话会
  // 逼消费端去反解文案。
  it("states a machine-discriminable reject reason and the active turn count on a refused self-update", () => {
    const refused = ProtobufRpcCodec.encodeTypedMethodResponse(
      4n,
      rpcMethods.agentredSelfUpdate,
      {
        accepted: false,
        rejectReason: AgentredSelfUpdateRejectReason.ACTIVE_TURNS,
        activeTurns: 2,
        message: "2 turns are still running",
      },
    );
    expect(
      decodeRpcMethodResponse(refused, rpcMethods.agentredSelfUpdate),
    ).toMatchObject({
      accepted: false,
      rejectReason: AgentredSelfUpdateRejectReason.ACTIVE_TURNS,
      activeTurns: 2,
    });
    expect(AgentredSelfUpdateRejectReason.IN_PROGRESS).not.toBe(
      AgentredSelfUpdateRejectReason.ACTIVE_TURNS,
    );
  });

  // 决策 4:daemon 自报的是**版本号**与**短 commit** 两个独立字段,而不是
  // BuildIdentity() 那个「版本 (commit)」展示串 —— 解析展示串等于把展示格式变成
  // 契约。两处应答(桌面端心跳走 health.ping、server 建镜像连接走 auth.account)
  // 报的是同一对取值,所以两处都要有位置放它们。
  it("carries the daemon build version and short commit as two independent fields on both self-reporting responses", () => {
    const ping = ProtobufRpcCodec.encodeTypedMethodResponse(
      1n,
      rpcMethods.healthPing,
      { daemonVersion: "0.4.2", daemonCommit: "a1b2c3d" },
    );
    expect(decodeRpcMethodResponse(ping, rpcMethods.healthPing)).toMatchObject({
      daemonVersion: "0.4.2",
      daemonCommit: "a1b2c3d",
    });

    const account = ProtobufRpcCodec.encodeTypedMethodResponse(
      2n,
      rpcMethods.authAccount,
      { ok: true, daemonVersion: "0.4.2", daemonCommit: "a1b2c3d" },
    );
    expect(
      decodeRpcMethodResponse(account, rpcMethods.authAccount),
    ).toMatchObject({ daemonVersion: "0.4.2", daemonCommit: "a1b2c3d" });
  });

  // 决策 5 的协议前提:未注入 commit 的本地构建照报版本号(它自称 1.0.0),短 commit
  // 是空串。两者必须能被分别表达,否则「开发构建永不劝升」在协议层就无从判定。
  it("lets a build without an injected commit report a version alongside an empty short commit", () => {
    const ping = ProtobufRpcCodec.encodeTypedMethodResponse(
      3n,
      rpcMethods.healthPing,
      { daemonVersion: "1.0.0", daemonCommit: "" },
    );
    expect(decodeRpcMethodResponse(ping, rpcMethods.healthPing)).toMatchObject({
      daemonVersion: "1.0.0",
      daemonCommit: "",
    });
  });

  it("decodes only the response schema paired with the requested method", () => {
    const encoded = ProtobufRpcCodec.encodeTypedMethodResponse(
      3n,
      rpcMethods.remoteFsMkdir,
      { path: "/tmp/new" },
    );
    expect(
      decodeRpcMethodResponse(encoded, rpcMethods.remoteFsMkdir),
    ).toMatchObject({
      path: "/tmp/new",
    });
    expect(() =>
      decodeRpcMethodResponse(encoded, rpcMethods.engineDiscover),
    ).toThrow(/method ID/);
  });
});
