import { describe, expect, it } from "vitest";

import {
  createTranscriptProjector,
  interactiveRequestIds,
  reduceFrames,
  reduceSessionState,
  type TranscriptFrame,
} from "./frames";

/**
 * `reduceFrames`：wire 事件帧 → 本包的 `TranscriptMessage[]`。
 *
 * 这个名字不是新造的 —— `dto.ts` 开头就写着「桌面端把生成对象零转换直接喂进来，
 * **另一侧则由 `reduceFrames()` 把 wire 帧归约成同一形态**」。两个宿主此前各写了
 * 一份：agentre-server 的 `lib/transcriptFrames.ts` 归约成本 DTO，桌面端 Peer Tab 的
 * `peer-transcript.ts` 只认五种块、其余一律落自造的 `raw`（而本包的行模型根本没有
 * `raw` 这一档，于是它们最终渲染成一行 `(debug) unimplemented block type: raw`，
 * 载荷整个不可见）。这份用例连同实现一起从 agentre-server 搬进包里，两个宿主此后
 * 只保留各自的**帧来源**。
 *
 * 判据不是「长得像不像」，而是**归宿**：每一个 kind 落到哪。桌面端
 * `chat_svc/dispatcher_wiring.go` 给每个 kind 都注册了 handler，一个都没丢、一个都
 * 没当未知事件印 JSON —— 这份用例照着那张表钉，`frames-vocabulary.test.ts` 再机械地
 * 保证词表里一个 kind 都没漏。
 *
 * 归约成自造扁平结构那条老路的后果不是「少画了几种卡」，是三件同时发生的事：
 *   1. 遥测帧（usage / context_window_updated / runtime_status）被当成「未知事件」
 *      整块 JSON 铺在正文里，而它们每次 API call 都来一条；
 *   2. 每铺一张就 flush 一次消息段，于是**一轮助手被劈成好几条**，各自长出一个
 *      头像 +「Assistant」抬头；
 *   3. 宿主自画的块不在本包的对话列里，左右都探出去。
 */

let seq = 0;
function f(event: Record<string, unknown>): TranscriptFrame {
  return { sessionId: 1, event, seq: ++seq };
}

const SID = 1;

describe("reduceFrames:消息与正文", () => {
  it("给定用户消息帧，当归约，则出一条 user 消息并带上来源设备的指纹与显示名", () => {
    // 指纹与显示名都要留：buildSourceByMessageId 拿**指纹**跟本机比对，
    // 只有「不是这台浏览器发的」才标来源。此前 server 宿主自建表、不做比对，于是你
    // 自己发出去的消息也挂着「From Chrome on macOS」。
    const [msg] = reduceFrames(
      [
        f({
          kind: "user_message",
          text: "帮我看看",
          sourceDevice: "fp-browser-1",
          sourceDeviceName: "Chrome on macOS",
        }),
      ],
      SID,
    );

    expect(msg.role).toBe("user");
    expect(msg.sessionId).toBe(SID);
    expect(msg.blocks).toEqual([{ type: "text", text: "帮我看看" }]);
    expect(msg.sourceDevice).toBe("fp-browser-1");
    expect(msg.sourceDeviceName).toBe("Chrome on macOS");
  });

  it("给定连续多个 text_delta，当归约，则合成**一条**助手消息里的一个 text 块", () => {
    // 一轮里会有很多段文本增量。每段各起一条消息 = 每段长一个头像列。
    const msgs = reduceFrames(
      [
        f({ kind: "text_delta", text: "我先" }),
        f({ kind: "text_delta", text: "读一下" }),
        f({ kind: "text_delta", text: "路由表。" }),
      ],
      SID,
    );

    expect(msgs.length).toBe(1);
    expect(msgs[0].role).toBe("assistant");
    expect(msgs[0].blocks).toEqual([
      { type: "text", text: "我先读一下路由表。" },
    ]);
  });

  it("给定 thinking 与正文交替，当归约，则同一条消息里两种块各自累积、顺序保持", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "thinking_delta", text: "先读 " }),
        f({ kind: "thinking_delta", text: "router.go。" }),
        f({ kind: "text_delta", text: "看到了。" }),
      ],
      SID,
    );

    expect(msg.blocks).toEqual([
      { type: "thinking", text: "先读 router.go。" },
      { type: "text", text: "看到了。" },
    ]);
  });

  it("给定 done 之后又来正文，当归约，则另起一条助手消息", () => {
    const msgs = reduceFrames(
      [
        f({ kind: "text_delta", text: "好的。" }),
        f({ kind: "done" }),
        f({ kind: "text_delta", text: "接着说。" }),
      ],
      SID,
    );

    expect(msgs.map((m) => m.blocks)).toEqual([
      [{ type: "text", text: "好的。" }],
      [{ type: "text", text: "接着说。" }],
    ]);
  });

  it("给定用户消息，当其后紧跟助手正文，则不会被吸进用户那条", () => {
    const msgs = reduceFrames(
      [
        f({ kind: "user_message", text: "帮我看看" }),
        f({ kind: "text_delta", text: "好的。" }),
      ],
      SID,
    );

    expect(msgs.map((m) => m.role)).toEqual(["user", "assistant"]);
  });

  it("给定 error 帧，当归约，则落在消息的 errorText 上而不是新起一个块", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "text_delta", text: "好的。" }),
        f({ kind: "error", message: "connection reset by peer" }),
      ],
      SID,
    );

    expect(msg.errorText).toBe("connection reset by peer");
    expect(msg.blocks.map((b) => b.type)).toEqual(["text"]);
  });
});

describe("reduceFrames:工具", () => {
  it("给定工具调用与结果，当归约，则同一条消息里出 tool_use + tool_result，按 toolUseId 配对", () => {
    const [msg] = reduceFrames(
      [
        f({
          kind: "tool_use_start",
          id: "c1",
          name: "Read",
          input: { file_path: "a.go" },
        }),
        f({ kind: "tool_result", toolCallId: "c1", content: "package a" }),
      ],
      SID,
    );

    expect(msg.blocks).toEqual([
      {
        type: "tool_use",
        toolUseId: "c1",
        toolName: "Read",
        toolInput: { file_path: "a.go" },
      },
      {
        type: "tool_result",
        toolUseId: "c1",
        text: "package a",
        isError: false,
      },
    ]);
  });

  it("给定同一轮起了两个工具，当结果乱序回来，则各自认自己的 toolUseId", () => {
    // 一条助手消息可以同时起 Read + Grep。只看「最后一张未完成的卡」会把 Read 的
    // 输出挂到 Grep 上 —— 用户读到张冠李戴的工具输出。
    const [msg] = reduceFrames(
      [
        f({ kind: "tool_use_start", id: "read", name: "Read", input: {} }),
        f({ kind: "tool_use_start", id: "grep", name: "Grep", input: {} }),
        f({ kind: "tool_result", toolCallId: "grep", content: "grep 的输出" }),
        f({ kind: "tool_result", toolCallId: "read", content: "read 的输出" }),
      ],
      SID,
    );

    const results = msg.blocks.filter((b) => b.type === "tool_result");
    expect(results.map((b) => [b.toolUseId, b.text])).toEqual([
      ["grep", "grep 的输出"],
      ["read", "read 的输出"],
    ]);
  });

  it("给定工具结果标了 isError，当归约，则如实带上", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "tool_use_start", id: "c1", name: "Bash", input: {} }),
        f({
          kind: "tool_result",
          toolCallId: "c1",
          content: "FAIL",
          isError: true,
        }),
      ],
      SID,
    );

    expect(msg.blocks[1].isError).toBe(true);
  });
});

describe("reduceFrames:审批与提问 —— 决议要回填原卡，不是另起一张", () => {
  it("给定工具授权请求，当归约，则出带 canonical 的 tool_permission_request 块", () => {
    // canonical 是本包那些交互卡渲染的前提：没有它，CanonicalToolRouter 回落到
    // RawToolCard，端口也永远不会被调到。server 宿主的老实现一个 canonical 都不产。
    const [msg] = reduceFrames(
      [
        f({
          kind: "tool_permission_request",
          requestId: "r9",
          toolName: "Edit",
          input: { file_path: "a.go" },
        }),
      ],
      SID,
    );

    const block = msg.blocks[0];
    expect(block.type).toBe("tool_permission_request");
    expect(block.toolPermission).toEqual({
      requestId: "r9",
      toolName: "Edit",
      toolInput: { file_path: "a.go" },
    });
    expect(block.canonical?.kind).toBe("tool.permission");
    expect(block.canonical?.toolPermission?.requestId).toBe("r9");
  });

  it("给定授权被批准，当归约，则**回填**那一张卡而不是新增一个块", () => {
    // 这正是用户看到「未知事件 · tool_permission_resolved」的那一条。桌面端
    // ToolPermissionResolvedHandler 是 turn.Mutate 回同一条块，卡片切只读态。
    const [msg] = reduceFrames(
      [
        f({
          kind: "tool_permission_request",
          requestId: "r9",
          toolName: "Edit",
          input: {},
        }),
        f({ kind: "tool_permission_resolved", requestId: "r9", allowed: true }),
      ],
      SID,
    );

    expect(msg.blocks.length).toBe(1);
    expect(msg.blocks[0].toolPermission).toMatchObject({
      requestId: "r9",
      resolved: true,
      allowed: true,
    });
    expect(msg.blocks[0].canonical?.toolPermission).toMatchObject({
      resolved: true,
      allowed: true,
    });
  });

  it("给定授权被拒绝，当归约，则回填 allowed=false", () => {
    const [msg] = reduceFrames(
      [
        f({
          kind: "tool_permission_request",
          requestId: "r9",
          toolName: "Edit",
          input: {},
        }),
        f({
          kind: "tool_permission_resolved",
          requestId: "r9",
          allowed: false,
        }),
      ],
      SID,
    );

    expect(msg.blocks[0].toolPermission).toMatchObject({
      resolved: true,
      allowed: false,
    });
  });

  it("给定决议先到而请求没见过，当归约，则不凭空造一张卡", () => {
    // 桌面端 Mutate 命中不了就 return nil。造一张只有 resolved 没有 toolName
    // 的空卡，比不画更糟。
    const msgs = reduceFrames(
      [
        f({
          kind: "tool_permission_resolved",
          requestId: "ghost",
          allowed: true,
        }),
      ],
      SID,
    );

    expect(msgs.flatMap((m) => m.blocks)).toEqual([]);
  });

  it("给定提问与作答，当归约，则同样回填到那一张提问卡上", () => {
    const [msg] = reduceFrames(
      [
        f({
          kind: "ask_user_question",
          requestId: "q1",
          questions: [{ question: "要不要顺手改 e2e？", options: [] }],
        }),
        f({
          kind: "ask_user_question_answered",
          requestId: "q1",
          answers: [{ questionIndex: 0, labels: ["要"] }],
        }),
      ],
      SID,
    );

    expect(msg.blocks.length).toBe(1);
    expect(msg.blocks[0].type).toBe("ask_user_question");
    expect(msg.blocks[0].askUserQuestion).toMatchObject({
      requestId: "q1",
      answered: true,
      answers: [{ questionIndex: 0, labels: ["要"] }],
    });
  });
});

describe("reduceFrames:记账帧不进正文", () => {
  /**
   * 这一组是本轮的核心。这些 kind 桌面端全都有归宿，但**都不在转录正文里**：
   * usage 落消息的 token 列、context_window 落会话、runtime_status 是过渡态
   * （handler 里明写「不落 block，不入 history」）、permission_mode 落会话字段。
   * 两个宿主的老实现都把它们铺成「未知事件」卡，而 usage 每次 API call
   * 就来一条 —— 真实会话里 JSON 会比正文还多。
   */
  it.each([
    ["context_window_updated", { used: 42000, total: 200000 }],
    ["runtime_status", { status: "compacting" }],
    ["permission_mode_changed", { mode: "acceptEdits" }],
    ["steer_consumed", {}],
    ["tool_use_end", { id: "c1" }],
    ["retry", { attempt: 1, maxAttempts: 3 }],
  ])("给定 %s 帧，当归约，则不产生任何块", (kind, extra) => {
    const msgs = reduceFrames(
      [
        f({ kind: "text_delta", text: "正文" }),
        f({ kind, ...extra }),
        f({ kind: "text_delta", text: "继续" }),
      ],
      SID,
    );

    // 而且不能把一轮劈成两条：记账帧夹在中间不该终止当前消息。
    expect(msgs.length).toBe(1);
    expect(msgs[0].blocks).toEqual([{ type: "text", text: "正文继续" }]);
  });

  it("给定 usage 帧，当归约，则写进消息的 token 列而不是一个块", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "text_delta", text: "正文" }),
        f({
          kind: "usage",
          usage: {
            promptTokens: 1200,
            completionTokens: 340,
            cachedTokens: 90,
          },
        }),
      ],
      SID,
    );

    expect(msg.blocks.map((b) => b.type)).toEqual(["text"]);
    expect(msg.promptTokens).toBe(1200);
    expect(msg.completionTokens).toBe(340);
    expect(msg.cachedTokens).toBe(90);
  });

  it("给定压缩边界帧，当归约，则落成本包认得的 compact_boundary 块", () => {
    // 这一条与上面几条相反：桌面端 CompactBoundaryHandler 是**落 block** 的，
    // 而且包里就有 CompactBoundaryDivider 组件。
    const [msg] = reduceFrames(
      [
        f({ kind: "text_delta", text: "正文" }),
        f({ kind: "compact_boundary", preTokens: 120000, trigger: "auto" }),
      ],
      SID,
    );

    const compact = msg.blocks.find((b) => b.type === "compact_boundary");
    expect(compact?.compact).toMatchObject({
      preTokens: 120000,
      trigger: "auto",
    });
  });
});

describe("reduceFrames:真正不认识的才算未知", () => {
  it("给定词表外的 kind，当归约，则落 unknown 块并原样带上载荷（R8）", () => {
    // kindOf 是断言不是校验：比本仓新的 daemon、坏帧都可能来。这时如实呈现，
    // 但**只有这一类**才算未知 —— 上面那些是「认得，但归宿不在正文」。
    const [msg] = reduceFrames(
      [f({ kind: "brand_new_kind_from_a_newer_daemon", payload: 42 })],
      SID,
    );

    const block = msg.blocks[0];
    // `raw` 是给消费方的结构化载荷。
    expect(block.raw).toMatchObject({
      kind: "brand_new_kind_from_a_newer_daemon",
      payload: 42,
    });
    // 但**画得出来**才算「如实呈现」。本包的 `unknown` 块只画一行
    // `(debug) unimplemented block type: unknown`，压根不读 raw —— 用它就等于
    // 把载荷藏了，正是 R8 要拦的。落 `notice`：本包的 notice 分支原样渲染
    // block.text，宽度是 max-w-measure（在对话列内），底色 muted 不抢正文。
    expect(block.type).toBe("notice");
    expect(block.text).toContain("brand_new_kind_from_a_newer_daemon");
    expect(block.text).toContain("42");
  });

  it("给定载荷压根不是对象，当归约，则同样如实落 unknown 而不是抛错", () => {
    const msgs = reduceFrames([{ sessionId: SID, event: "坏帧", seq: 1 }], SID);

    expect(msgs[0].blocks[0].type).toBe("notice");
    expect(msgs[0].blocks[0].text).toContain("坏帧");
  });
});

describe("reduceFrames:重新归约要稳定", () => {
  it("给定同一段前缀，当整段重新归约，则消息 id 逐个不变", () => {
    // SessionDetail 每来一条实时帧就把整段事件流重新归约一次。id 是行 key，
    // 一漂就是整段 unmount/remount：文本选中被清、滚动锚点丢失。
    const stream = [
      f({ kind: "user_message", text: "一" }),
      f({ kind: "text_delta", text: "二" }),
      f({ kind: "done" }),
    ];

    const first = reduceFrames(stream, SID);
    const second = reduceFrames(
      [...stream, f({ kind: "text_delta", text: "三" })],
      SID,
    );

    expect(second.slice(0, first.length).map((m) => m.id)).toEqual(
      first.map((m) => m.id),
    );
  });
});

describe("interactiveRequestIds:跟 waiters 去重", () => {
  it("给定未决的授权与提问，当取集合，则两个 requestId 都在", () => {
    const messages = reduceFrames(
      [
        f({
          kind: "tool_permission_request",
          requestId: "r9",
          toolName: "Edit",
          input: {},
        }),
        f({ kind: "ask_user_question", requestId: "q1", questions: [] }),
      ],
      SID,
    );

    expect([...interactiveRequestIds(messages)].sort()).toEqual(["q1", "r9"]);
  });

  it("给定已决议的授权，当取集合，则不再算它 —— 只读卡不占 waiters", () => {
    const messages = reduceFrames(
      [
        f({
          kind: "tool_permission_request",
          requestId: "r9",
          toolName: "Edit",
          input: {},
        }),
        f({ kind: "tool_permission_resolved", requestId: "r9", allowed: true }),
      ],
      SID,
    );

    expect(interactiveRequestIds(messages).size).toBe(0);
  });

  it("给定事件流里压根没有那条待决，当取集合，则它不在 —— DecisionPanel 仍要兜住", () => {
    // 镜像日志被裁剪、或浏览器从中途接进来：waiters 有、事件流没有。
    const messages = reduceFrames(
      [f({ kind: "text_delta", text: "正文" })],
      SID,
    );

    expect(interactiveRequestIds(messages).has("r-only-in-waiters")).toBe(
      false,
    );
  });
});

/**
 * 会话级状态（2026-08-20 对话页 UI/UX 改版）。
 *
 * `context_window_updated` / `permission_mode_changed` 这两类帧在
 * `reduceFrames` 里是**记而不显**的：它们的归宿不在转录正文里，而在会话状态与
 * Composer 上（文件里那段注释的原话是「缺的是显示面，不是数据」）。Composer 底栏
 * 现在就是那层显示面，所以把它们单独归约出来 —— 仍然不进正文，`reduceFrames`
 * 一个字都不改。
 */
describe("reduceSessionState:会话级状态", () => {
  it("上下文窗口取最后一次上报的值（runtime 探到会变，比如 Pi 用 get_session_stats 校正）", () => {
    const st = reduceSessionState([
      f({ kind: "context_window_updated", tokens: 128000 }),
      f({ kind: "context_window_updated", tokens: 200000 }),
    ]);

    expect(st.contextWindow).toBe(200000);
  });

  it("tokens=0 是「没探到」，不是「窗口为 0」——不覆盖已经探到的那个值", () => {
    const st = reduceSessionState([
      f({ kind: "context_window_updated", tokens: 200000 }),
      f({ kind: "context_window_updated", tokens: 0 }),
    ]);

    expect(st.contextWindow).toBe(200000);
  });

  it("usage 帧上也带 contextWindow：没有单独那条帧时由它兜住", () => {
    const st = reduceSessionState([
      f({ kind: "usage", totalInputTokens: 100, contextWindow: 64000 }),
    ]);

    expect(st.contextWindow).toBe(64000);
  });

  it("一条相关的帧都没有时是 0（不编一个窗口大小出来）", () => {
    const st = reduceSessionState([f({ kind: "text_delta", text: "你好" })]);

    expect(st.contextWindow).toBe(0);
  });

  it("权限模式取最后一次 runtime 上报的值", () => {
    const st = reduceSessionState([
      f({ kind: "permission_mode_changed", mode: "plan" }),
      f({ kind: "permission_mode_changed", mode: "acceptEdits" }),
    ]);

    expect(st.permissionMode).toBe("acceptEdits");
  });
});

/**
 * 增量投影：把「整段流每来一帧重算一次」换成「只归约新到的那几帧」。
 *
 * 为什么这件事值得单独有一层：`reduceFrames` 每次都从空状态重建**全部**
 * TranscriptMessage 对象。文件开头那段注释解释了它为什么能这么做（id 按出现顺序
 * 分配，前缀相同则 id 逐个相同，行 key 因此稳定）——但 key 稳定不等于**引用**稳定，
 * 而下游吃的正是引用：
 *
 *   - `TranscriptRowView` 是 `React.memo`，它的行对象来自一个以
 *     `TranscriptMessage` 为键的 WeakMap 缓存。每帧换一批新消息对象 = 每帧全表 miss
 *     = 1500 行组件全部重渲染；
 *   - 助手正文在流式期间每来一个 token 就把整段 markdown 重新解析一遍
 *     （remark-gfm + highlight.js 的语言自动探测），总工作量 O(n²)。
 *
 * 所以这里钉的性质只有一条：**没被这一批帧改到的消息，引用必须原样不动**。
 */
describe("增量投影:只重建被改到的那条消息", () => {
  it("追加一帧,先前定稿的消息保持同一个引用,被改到的那条换新身份", () => {
    const projector = createTranscriptProjector(SID);
    const settled = [
      f({ kind: "user_message", text: "hi" }),
      f({ kind: "text_delta", text: "a" }),
    ];

    const first = projector.project(settled);
    expect(first).toHaveLength(2);
    expect(first[1].blocks).toEqual([{ type: "text", text: "a" }]);

    const second = projector.project([
      ...settled,
      f({ kind: "text_delta", text: "b" }),
    ]);

    expect(second[0]).toBe(first[0]);
    expect(second[1]).not.toBe(first[1]);
    expect(second[1].blocks).toEqual([{ type: "text", text: "ab" }]);
    // 数组本身也必须换新,否则 useMemo 的下游看不出有变化。
    expect(second).not.toBe(first);
  });

  it("一轮结束后开新的一轮,上一轮那条彻底不再变身份", () => {
    const projector = createTranscriptProjector(SID);
    const firstTurn = [
      f({ kind: "user_message", text: "hi" }),
      f({ kind: "text_delta", text: "answer" }),
      f({ kind: "done" }),
    ];
    const before = projector.project(firstTurn);
    const after = projector.project([
      ...firstTurn,
      f({ kind: "user_message", text: "again" }),
      f({ kind: "text_delta", text: "second" }),
    ]);

    expect(after[0]).toBe(before[0]);
    expect(after[1]).toBe(before[1]);
    expect(after).toHaveLength(4);
  });

  it("回填历史块时,承载它的那条消息必须换新身份", () => {
    // 提问卡的答案回填的是**先前**某条消息里的块(findBlock 从后往前找,不经
    // openAssistant)。它确实改了那条消息,所以那条必须换身份——否则下游的行缓存
    // 会继续交出回填之前的那一份,答案永远画不出来。
    const projector = createTranscriptProjector(SID);
    const opening = [
      f({ kind: "ask_user_question", requestId: "q1", questions: [] }),
      f({ kind: "done" }),
    ];
    const before = projector.project(opening);
    expect(before[0].blocks[0].askUserQuestion?.answered).toBeFalsy();

    const after = projector.project([
      ...opening,
      f({ kind: "ask_user_question_answered", requestId: "q1", answers: [] }),
    ]);

    expect(after[0]).not.toBe(before[0]);
    expect(after[0].blocks[0].askUserQuestion?.answered).toBe(true);
  });

  it("帧数组不是上一段的延长时整段重算", () => {
    // 首屏加载、切会话、镜像日志被裁剪都会整段换掉。
    const projector = createTranscriptProjector(SID);
    projector.project([f({ kind: "user_message", text: "old" })]);
    const fresh = projector.project([f({ kind: "user_message", text: "new" })]);

    expect(fresh).toHaveLength(1);
    expect(fresh[0].blocks).toEqual([{ type: "text", text: "new" }]);
  });

  it("同一个数组重复投影是幂等的", () => {
    // React StrictMode 下 useMemo 的工厂会跑两次。第二次不能把帧再消费一遍。
    const projector = createTranscriptProjector(SID);
    const frames = [
      f({ kind: "user_message", text: "hi" }),
      f({ kind: "text_delta", text: "a" }),
    ];
    const once = projector.project(frames);
    const twice = projector.project(frames);

    expect(twice).toBe(once);
    expect(twice[1].blocks).toEqual([{ type: "text", text: "a" }]);
  });

  it("投影结果与整段重算完全一致", () => {
    // 增量只是省算,不能改答案。
    const frames = [
      f({ kind: "user_message", text: "hi" }),
      f({ kind: "thinking_delta", text: "think" }),
      f({ kind: "text_delta", text: "one" }),
      f({ kind: "tool_use", id: "c1", name: "read", input: { path: "a" } }),
      f({ kind: "tool_result", id: "c1", content: "ok" }),
      f({ kind: "text_delta", text: "two" }),
      f({ kind: "done" }),
    ];
    const projector = createTranscriptProjector(SID);
    let incremental: ReturnType<typeof reduceFrames> = [];
    for (let i = 1; i <= frames.length; i++) {
      incremental = projector.project(frames.slice(0, i));
    }

    expect(incremental).toEqual(reduceFrames(frames, SID));
  });
});

/**
 * 一轮的 meta（模型 · 耗时 · 首字 · 速率）。
 *
 * 这一段以前整块是空的，`emptyMessage` 的注释把理由写得很直白：「中转事件流不带
 * 用量总计 / 耗时 / 时间戳，这些字段一律零值……真要显示得让中转链路先把这些数据
 * 传过来，不能在这一层编」。链路现在传过来了 —— agentred 就着它自己扇出的那条事件
 * 流量表（口径与桌面端 chat_svc 共用 internal/pkg/turnstats），把三个数盖在
 * `runtime.runResultDone` 终态帧上，宿主把它随 `done` 交进来。
 *
 * 这里仍然一个数都不编：帧上没有的就保持零值，而零值是「不显示」。
 */
describe("reduceFrames:一轮的 meta", () => {
  it("给定终态 done 帧，当归约，则模型与计时落在这一轮的助手消息上", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "text_delta", text: "正文" }),
        f({
          kind: "done",
          model: "claude-sonnet-4-6",
          durationMs: 9640,
          firstTokenMs: 8010,
          tokensPerSec: 14.2,
        }),
      ],
      SID,
    );

    expect(msg.model).toBe("claude-sonnet-4-6");
    expect(msg.durationMs).toBe(9640);
    expect(msg.firstTokenMs).toBe(8010);
    expect(msg.tokensPerSec).toBeCloseTo(14.2);
    // 终态帧本身不是正文,一个块都不该多出来。
    expect(msg.blocks.map((b) => b.type)).toEqual(["text"]);
  });

  it("给定 done 帧带 usage，当归约，则补齐没有 usage 帧的那些后端的用量", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "text_delta", text: "正文" }),
        f({
          kind: "done",
          usage: {
            promptTokens: 1200,
            completionTokens: 340,
            cachedTokens: 90,
          },
        }),
      ],
      SID,
    );

    expect(msg.promptTokens).toBe(1200);
    expect(msg.completionTokens).toBe(340);
    expect(msg.cachedTokens).toBe(90);
  });

  /**
   * completion / reasoning 由 usage 帧**逐跳累加** —— 一轮里每个内部 API call 都发
   * 一条，而 done 的 usage 是最后一跳的值，不是合计。桌面端 turn_run.go 那段写得
   * 很清楚：「completion / reasoning 由 usage 帧按调用累加；Done 的 usage 是最后
   * 一跳，不能覆盖合计。没有 usage 帧时才用 result 兜底」。此前这里两个都是覆盖，
   * 于是多跳的一轮在控制台上只报得出最后一跳的接收量。
   */
  it("给定多条 usage 帧，当归约，则 completion / reasoning 累加而 prompt 取最新", () => {
    const [msg] = reduceFrames(
      [
        f({
          kind: "usage",
          usage: {
            promptTokens: 1200,
            completionTokens: 340,
            reasoningTokens: 20,
            cachedTokens: 90,
          },
        }),
        f({
          kind: "usage",
          usage: {
            promptTokens: 1500,
            completionTokens: 60,
            reasoningTokens: 5,
            cachedTokens: 110,
          },
        }),
      ],
      SID,
    );

    expect(msg.completionTokens).toBe(400);
    expect(msg.reasoningTokens).toBe(25);
    expect(msg.promptTokens).toBe(1500);
    expect(msg.cachedTokens).toBe(110);
  });

  it("给定 usage 帧在前、done 的 usage 在后，当归约，则合计不被最后一跳覆盖", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "usage", usage: { completionTokens: 340 } }),
        f({ kind: "usage", usage: { completionTokens: 60 } }),
        f({
          kind: "done",
          usage: { promptTokens: 1500, completionTokens: 60 },
        }),
      ],
      SID,
    );

    expect(msg.completionTokens).toBe(400);
    expect(msg.promptTokens).toBe(1500);
  });

  /**
   * 出错收场的那一轮同样要有 meta：`error` 帧会把当前消息收掉（错误卡挂在末行，
   * 继续追加块会让它漂到后来的正文之后），而终态帧紧随其后 —— 若只认「还开着的」
   * 那条消息，报错的一轮就永远没有模型与耗时，而那恰恰是最需要看这两个数的时候。
   */
  it("给定 error 之后的 done 帧，当归约，则 meta 仍落在出错的那一轮上", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "text_delta", text: "正文" }),
        f({ kind: "error", message: "boom" }),
        f({ kind: "done", model: "gpt-5-codex", durationMs: 1200 }),
      ],
      SID,
    );

    expect(msg.errorText).toBe("boom");
    expect(msg.model).toBe("gpt-5-codex");
    expect(msg.durationMs).toBe(1200);
  });

  it("给定 done 帧但这一轮没有助手消息，当归约，则不凭空开一条空消息", () => {
    const msgs = reduceFrames(
      [
        f({ kind: "user_message", text: "在吗" }),
        f({ kind: "done", model: "gpt-5-codex", durationMs: 1200 }),
      ],
      SID,
    );

    expect(msgs.map((m) => m.role)).toEqual(["user"]);
  });

  it("给定光秃秃的 done 帧，当归约，则一个数都不编", () => {
    const [msg] = reduceFrames(
      [f({ kind: "text_delta", text: "正文" }), f({ kind: "done" })],
      SID,
    );

    expect(msg.model).toBe("");
    expect(msg.durationMs).toBe(0);
    expect(msg.firstTokenMs).toBeUndefined();
    expect(msg.tokensPerSec).toBeUndefined();
  });
});

/**
 * 零值读作「没上报」，不是「这一轮零耗时」。
 *
 * 两个生产者填的是不同的载体：桌面端 chat_svc 填 `done` 事件本身（它在 runtime
 * 之上收口，手里就有），agentred 填 `runtime.runResultDone` 终态帧（它在事件流
 * 之上量表，知道结果时 `done` 早转发出去了）。于是同一段流里可能先来一条 runtime
 * 自己 emit 的**空** `Done`、再来一条带数的 —— 空的那条不能把带数的抹掉。
 */
describe("reduceFrames:done 上的零值", () => {
  it("给定带数的 done 之后又来一条空 done，当归约，则数还在", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "text_delta", text: "正文" }),
        f({
          kind: "done",
          model: "glm-5.3",
          durationMs: 9640,
          firstTokenMs: 8010,
          tokensPerSec: 14.2,
        }),
        f({
          kind: "done",
          model: "",
          durationMs: 0,
          firstTokenMs: 0,
          tokensPerSec: 0,
        }),
      ],
      SID,
    );

    expect(msg.model).toBe("glm-5.3");
    expect(msg.durationMs).toBe(9640);
    expect(msg.firstTokenMs).toBe(8010);
    expect(msg.tokensPerSec).toBeCloseTo(14.2);
  });

  it("给定只有零值的 done，当归约，则一格都不填", () => {
    const [msg] = reduceFrames(
      [
        f({ kind: "text_delta", text: "正文" }),
        f({ kind: "done", durationMs: 0, firstTokenMs: 0, tokensPerSec: 0 }),
      ],
      SID,
    );

    expect(msg.durationMs).toBe(0);
    expect(msg.firstTokenMs).toBeUndefined();
    expect(msg.tokensPerSec).toBeUndefined();
  });
});

/**
 * subagent 派遣卡。此前这条路对四个 subagent 事件是**明确 return 不处理**的，
 * 于是 server 与桌面 Peer Tab 上根本没有派遣卡 —— 桌面自己的会话有（Go 侧
 * chat_svc/handlers/subagent.go 累计进 SubagentStateBlock，再投影进外层那张
 * tool_use 卡），两个宿主同一个组件、不同的喂法，只有这一侧是空的。
 *
 * 对齐的判据是**同一张卡读得到同样的字段**：`AgentSpawnCard` 的 `readSpawn` 要
 * `canonical.agentSpawn`（静态字段）叠 `block.subagent`（运行时累计），少任何一半
 * 都渲染不出来。累计规则照搬 Go 那一份：零值不覆盖（R4/R10）、模型 first-wins（R3）、
 * 前台 bash 不建 overlay、同 task_id 换 tool call 归一到原卡。
 */
describe("reduceFrames:subagent 派遣卡", () => {
  const agentToolUse = (id: string, input: Record<string, unknown> = {}) =>
    f({
      kind: "tool_use_start",
      id,
      name: "Agent",
      input,
      canonical: {
        kind: "agent.spawn",
        taskDescription: "Fact-check spec",
        subagentType: "general-purpose",
        prompt: "go check",
      },
    });

  it("给定 Agent 工具调用与它的生命周期帧，当归约，则外层那张卡同时拿到 canonical 与运行时累计", () => {
    const [msg] = reduceFrames(
      [
        agentToolUse("tu-A"),
        f({
          kind: "subagent_started",
          toolCallId: "tu-A",
          info: {
            taskId: "T",
            kind: "local_agent",
            taskDescription: "Fact-check spec",
          },
        }),
        f({
          kind: "subagent_progress",
          toolCallId: "tu-A",
          info: {
            taskId: "T",
            toolUses: 12,
            totalTokens: 3400,
            lastToolName: "Read",
          },
        }),
        f({
          kind: "subagent_model",
          toolCallId: "tu-A",
          model: "claude-opus-5",
        }),
        f({
          kind: "subagent_done",
          toolCallId: "tu-A",
          info: {
            taskId: "T",
            status: "completed",
            toolUses: 13,
            durationMs: 4200,
            summary: "报告",
          },
        }),
      ],
      SID,
    );

    const block = msg.blocks.find((b) => b.toolUseId === "tu-A");
    expect(block?.canonical?.kind).toBe("agent.spawn");
    expect(block?.canonical?.agentSpawn?.taskDescription).toBe(
      "Fact-check spec",
    );
    expect(block?.subagent).toMatchObject({
      taskId: "T",
      kind: "local_agent",
      status: "completed",
      toolUses: 13,
      totalTokens: 3400,
      durationMs: 4200,
      lastToolName: "Read",
      model: "claude-opus-5",
      summary: "报告",
    });
  });

  it("给定子代理内层的工具帧，当归约，则带上父调用与 run id，好归进派遣卡的 STEPS", () => {
    const [msg] = reduceFrames(
      [
        agentToolUse("tu-A"),
        f({
          kind: "tool_use_start",
          id: "tu-inner",
          name: "Read",
          input: { file_path: "/a.md" },
          parentToolCallId: "tu-A",
          subagentRunId: "run-1",
        }),
        f({
          kind: "tool_result",
          toolCallId: "tu-inner",
          content: "ok",
          parentToolCallId: "tu-A",
          subagentRunId: "run-1",
        }),
      ],
      SID,
    );

    const use = msg.blocks.find(
      (b) => b.toolUseId === "tu-inner" && b.type === "tool_use",
    );
    const result = msg.blocks.find(
      (b) => b.toolUseId === "tu-inner" && b.type === "tool_result",
    );
    expect(use?.parentToolUseId).toBe("tu-A");
    expect(use?.subagentRunId).toBe("run-1");
    expect(result?.parentToolUseId).toBe("tu-A");
    expect(result?.subagentRunId).toBe("run-1");
  });

  it("给定普通前台 Bash 的 task 帧，当归约，则不给它挂 overlay（否则污染后台任务面板）", () => {
    const [msg] = reduceFrames(
      [
        f({
          kind: "tool_use_start",
          id: "tu-bash",
          name: "Bash",
          input: { command: "ls" },
        }),
        f({
          kind: "subagent_started",
          toolCallId: "tu-bash",
          info: { taskId: "B", kind: "local_bash" },
        }),
      ],
      SID,
    );
    expect(
      msg.blocks.find((b) => b.toolUseId === "tu-bash")?.subagent,
    ).toBeUndefined();
  });

  it("给定 run_in_background 的 Bash，当归约，则照常建 overlay", () => {
    const [msg] = reduceFrames(
      [
        f({
          kind: "tool_use_start",
          id: "tu-bg",
          name: "Bash",
          input: { command: "sleep 20", run_in_background: true },
        }),
        f({
          kind: "subagent_started",
          toolCallId: "tu-bg",
          info: { taskId: "B", kind: "local_bash" },
        }),
      ],
      SID,
    );
    expect(
      msg.blocks.find((b) => b.toolUseId === "tu-bg")?.subagent?.status,
    ).toBe("running");
  });

  it("给定后续帧的计数是零值，当归约，则不抹掉已经攒起来的进度（protobuf 默认值读作「没上报」）", () => {
    const [msg] = reduceFrames(
      [
        agentToolUse("tu-A"),
        f({
          kind: "subagent_started",
          toolCallId: "tu-A",
          info: { taskId: "T", kind: "local_agent" },
        }),
        f({
          kind: "subagent_progress",
          toolCallId: "tu-A",
          info: { taskId: "T", toolUses: 9, totalTokens: 900 },
        }),
        f({
          kind: "subagent_done",
          toolCallId: "tu-A",
          info: {
            taskId: "T",
            status: "completed",
            toolUses: 0,
            totalTokens: 0,
            durationMs: 0,
            summary: "",
          },
        }),
      ],
      SID,
    );
    expect(
      msg.blocks.find((b) => b.toolUseId === "tu-A")?.subagent,
    ).toMatchObject({
      status: "completed",
      toolUses: 9,
      totalTokens: 900,
    });
  });

  it("给定同一个 task_id 换了新的 tool call（SendMessage 恢复），当归约，则归一到原卡并留下中断证据", () => {
    const [msg] = reduceFrames(
      [
        agentToolUse("tu-A"),
        f({
          kind: "subagent_started",
          toolCallId: "tu-A",
          info: { taskId: "T", kind: "local_agent" },
        }),
        f({
          kind: "subagent_done",
          toolCallId: "tu-A",
          info: {
            taskId: "T",
            status: "failed",
            toolUses: 61,
            summary: "API error",
          },
        }),
        f({
          kind: "tool_use_start",
          id: "tu-B",
          name: "SendMessage",
          input: { to: "spec-factcheck" },
        }),
        f({
          kind: "subagent_started",
          toolCallId: "tu-B",
          info: { taskId: "T", kind: "local_agent" },
        }),
        f({
          kind: "subagent_done",
          toolCallId: "tu-B",
          info: { taskId: "T", status: "completed", summary: "报告" },
        }),
      ],
      SID,
    );

    const withOverlay = msg.blocks.filter((b) => b.subagent);
    expect(withOverlay).toHaveLength(1);
    expect(withOverlay[0].toolUseId).toBe("tu-A");
    expect(withOverlay[0].subagent).toMatchObject({
      status: "completed",
      summary: "报告",
      toolUses: 61,
    });
    expect(withOverlay[0].subagent?.resumes).toEqual([
      { status: "failed", summary: "API error" },
    ]);
  });

  it("给定一轮收尾时前台派遣还挂在 running，当归约，则翻成 canceled；后台的照常留着跑", () => {
    const [msg] = reduceFrames(
      [
        agentToolUse("tu-fg", { run_in_background: false }),
        f({
          kind: "subagent_started",
          toolCallId: "tu-fg",
          info: { taskId: "T1", kind: "local_agent" },
        }),
        agentToolUse("tu-bg"),
        f({
          kind: "subagent_started",
          toolCallId: "tu-bg",
          info: { taskId: "T2", kind: "local_agent" },
        }),
        f({ kind: "done" }),
      ],
      SID,
    );
    expect(
      msg.blocks.find((b) => b.toolUseId === "tu-fg")?.subagent?.status,
    ).toBe("canceled");
    expect(
      msg.blocks.find((b) => b.toolUseId === "tu-bg")?.subagent?.status,
    ).toBe("running");
  });
});
