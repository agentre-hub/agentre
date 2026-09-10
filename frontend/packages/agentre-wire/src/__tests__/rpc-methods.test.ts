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
    //
    // 61–64 是端口转发的**声明族**(列举/新增/启停/删除):控制台的设备卡要列它、
    // 改它,所以浏览器侧有调用方。同族的**流族**(65–68:open/write/close/ack)
    // 刻意不在这张表里 —— 转发流由服务端那侧的 Go 代理与设备对接,浏览器发出的是
    // 普通 HTTP 请求,它一个字节都不经这条 RPC 通道。
    expect(
      Object.values(rpcMethods)
        .map((method) => method.id)
        .sort((a, b) => a - b),
    ).toEqual([
      ...Array.from({ length: 51 }, (_, index) => index + 1),
      57,
      58,
      59,
      60,
      61,
      62,
      63,
      64,
    ]);
  });

  // 端口转发声明族:控制台按 id 停用与删除,按端口 + 名称新增。这张表是手写的,
  // 所以四条都与生成的枚举直接对钉。
  it("pairs every port forward declaration descriptor with its generated proto ID", () => {
    expect(rpcMethods.portForwardList.id).toBe(RpcMethod.PORT_FORWARD_LIST);
    expect(rpcMethods.portForwardCreate.id).toBe(RpcMethod.PORT_FORWARD_CREATE);
    expect(rpcMethods.portForwardSetEnabled.id).toBe(
      RpcMethod.PORT_FORWARD_SET_ENABLED,
    );
    expect(rpcMethods.portForwardDelete.id).toBe(RpcMethod.PORT_FORWARD_DELETE);
  });

  // 一条映射的每一格都要过得了线:控制台拿 enabled 决定出不出「打开」,拿 port
  // 拼访问地址,拿 name 认它是哪个服务。
  it("round-trips a port forward mapping with its enabled bit and timestamps", () => {
    const listed = ProtobufRpcCodec.encodeTypedMethodResponse(
      5n,
      rpcMethods.portForwardList,
      {
        mappings: [
          {
            id: 7n,
            port: 3000,
            name: "dev server",
            // 启用位取 true 而不是 false:proto3 的隐式存在让 false 等同「这一格
            // 没写」,一个把 enabled 整个漏掉、或者映到另一个字段号的编码器,解出来
            // 照样是 false —— 用 false 钉不住这一格。下面那条停用的行补 false 这一
            // 侧(它要证的是「停用的声明整行保留」,不是字段映射对不对)。
            enabled: true,
            createtime: 1757000000n,
            updatetime: 1757000001n,
          },
          { id: 8n, port: 3001, name: "docs", enabled: false },
        ],
      },
    );
    const decoded = decodeRpcMethodResponse(
      listed,
      rpcMethods.portForwardList,
    ).mappings;
    expect(decoded[0]).toMatchObject({
      id: 7n,
      port: 3000,
      name: "dev server",
      enabled: true,
      // 时间戳过线是这条用例名字里就写着的一半,断言得真的问到它们:少了这两句,
      // createtime / updatetime 改字段号或整个掉出 PortForwardMapping 都不会红。
      createtime: 1757000000n,
      updatetime: 1757000001n,
    });
    // 停用是「保留声明、拒绝访问」:那一行整行都要还在。
    expect(decoded[1]).toMatchObject({ id: 8n, port: 3001, enabled: false });

    const encoded = encodeRpcMethodRequest(6n, rpcMethods.portForwardCreate, {
      port: 5173,
      name: "vite",
    });
    expect(ProtobufRpcCodec.decode(encoded).body).toMatchObject({
      case: "typedMethodRequest",
      methodId: RpcMethod.PORT_FORWARD_CREATE,
      method: "portForwardCreate",
      value: expect.objectContaining({ port: 5173, name: "vite" }),
    });
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

  // skill 命令清单:浏览器的输入框要靠它补全 —— 它必须落在本表里而不只是 Go 侧的
  // 方法常量,否则浏览器这条路只有类型、没有可发起的调用。
  it("pairs the skill commands descriptor with its generated proto ID", () => {
    expect(rpcMethods.skillCommands.id).toBe(RpcMethod.SKILLS_COMMANDS);
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
      // cwd 必须编得进请求:项目级 skill 只在那个目录下才解析得出来。
      [
        rpcMethods.skillCommands,
        { backendType: "claudecode", cwd: "/srv/project" },
      ],
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
