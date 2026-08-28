import {
  EventAskUserQuestion,
  EventCompactBoundary,
  EventContextWindowUpdated,
  EventExecApprovalRequested,
  EventExecApprovalResolved,
  EventOutputActivity,
  EventPlanUpdated,
  EventPermissionModeChanged,
  EventRetry,
  EventRuntimeStatus,
  EventSteerConsumed,
  EventSubagentDone,
  EventSubagentModel,
  EventSubagentProgress,
  EventSubagentStarted,
  EventTextDelta,
  EventToolPermissionRequest,
  EventUnrecognizedBlock,
  EventUsage,
  type EventKind,
} from "@agentre-hub/agentre-wire";
import { describe, it, expect } from "vitest";
import {
  createPeerTranscript,
  reducePeerEvent,
  reducePeerPullPage,
  type PeerEventFrame,
} from "../peer-transcript";

const frame = (
  seq: number,
  kind: EventKind,
  extra: Record<string, unknown> = {},
) =>
  ({
    fingerprint: "sha256:peer-desktop",
    sessionId: 7,
    seq,
    event: { kind, ...extra },
  }) satisfies PeerEventFrame;

describe("peer-transcript", () => {
  it("user_message opens a user row with source identity (R21)", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(
      s,
      frame(1, "user_message", {
        text: "帮我看看",
        sourceDevice: "sha256:browser",
        sourceDeviceName: "Chrome",
      }),
    );
    expect(s.messages).toHaveLength(1);
    const m = s.messages[0];
    expect(m.role).toBe("user");
    expect(m.blocks[0]).toMatchObject({ type: "text", text: "帮我看看" });
    expect(m.sourceDeviceName).toBe("Chrome");
  });

  it("text_delta accumulates into the current assistant message", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(s, frame(1, "user_message", { text: "hi" }));
    s = reducePeerEvent(s, frame(2, "text_delta", { text: "Hello" }));
    s = reducePeerEvent(s, frame(3, "text_delta", { text: " world" }));
    expect(s.messages).toHaveLength(2);
    expect(s.messages[1].role).toBe("assistant");
    expect(s.messages[1].blocks[0]).toMatchObject({
      type: "text",
      text: "Hello world",
    });
    expect(s.cursor).toBe(3);
  });

  it("done closes the assistant turn; the next assistant event opens a new row", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(s, frame(1, "user_message", { text: "hi" }));
    s = reducePeerEvent(s, frame(2, "text_delta", { text: "one" }));
    s = reducePeerEvent(s, frame(3, "done"));
    s = reducePeerEvent(s, frame(4, "user_message", { text: "again" }));
    s = reducePeerEvent(s, frame(5, "text_delta", { text: "two" }));
    expect(s.messages).toHaveLength(4);
    expect(s.messages[3].role).toBe("assistant");
    expect(s.messages[3].blocks[0]).toMatchObject({
      type: "text",
      text: "two",
    });
  });

  it("tool_use_start + tool_result pair into one assistant row", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(
      s,
      frame(1, "tool_use_start", { id: "t1", name: "Read" }),
    );
    s = reducePeerEvent(
      s,
      frame(2, "tool_result", { toolCallId: "t1", content: "file content" }),
    );
    expect(s.messages).toHaveLength(1);
    const m = s.messages[0];
    expect(m.blocks[0]).toMatchObject({
      type: "tool_use",
      toolUseId: "t1",
      toolName: "Read",
    });
    expect(m.blocks[1]).toMatchObject({
      type: "tool_result",
      toolUseId: "t1",
      text: "file content",
    });
  });

  it("ask_user_question becomes a pending decision; answered marks it", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(
      s,
      frame(1, "ask_user_question", {
        requestId: "req-1",
        questions: [
          { id: "q1", question: "继续?", header: "确认", options: [] },
        ],
      }),
    );
    expect(s.decisions).toHaveLength(1);
    expect(s.decisions[0]).toMatchObject({ kind: "ask", requestId: "req-1" });
    expect(s.waitingForInput).toBe(true);
    s = reducePeerEvent(
      s,
      frame(2, "ask_user_question_answered", {
        requestId: "req-1",
        skipped: false,
      }),
    );
    expect(s.decisions[0]).toMatchObject({ kind: "ask", answered: true });
    expect(s.waitingForInput).toBe(false);
  });

  it("tool_permission_request becomes a pending decision; resolved marks it", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(
      s,
      frame(1, "tool_permission_request", {
        requestId: "p-1",
        toolName: "Bash",
        toolCallId: "t1",
      }),
    );
    expect(s.decisions[0]).toMatchObject({
      kind: "permission",
      requestId: "p-1",
      toolName: "Bash",
    });
    s = reducePeerEvent(
      s,
      frame(2, "tool_permission_resolved", { requestId: "p-1", allowed: true }),
    );
    expect(s.decisions[0]).toMatchObject({
      kind: "permission",
      resolved: true,
      allowed: true,
    });
  });

  it("unknown kinds fall back to a notice block instead of being dropped (R8)", () => {
    let s = createPeerTranscript();
    // 真·词表外:比本仓新的对端发来的判别值。从前这里用的是 subagent_started ——
    // 那是词表**内**的 kind,只是当时没写 case,于是这条用例其实在给「遥测帧被
    // JSON 铺进正文」背书。现在遥测有自己的归宿,R8 得拿真的未知值来钉。
    //
    // 块类型从自造的 `raw` 换成共享包的 `notice`,是这次改用共享归约器的直接后果,
    // 而且是**修好一个 bug**:`raw` 不在包的行模型里,它走 default 分支渲染成一行
    // `(debug) unimplemented block type: raw`,压根不读载荷 —— 「不识别的如实呈现」
    // 从前只做到了归约器,渲染层把它藏了。`notice` 原样把 text 画出来。
    s = reducePeerEvent(
      s,
      frame(1, "future_kind_from_a_newer_peer" as EventKind),
    );
    const last = s.messages.at(-1)!;
    expect(last.blocks[0]).toMatchObject({ type: "notice" });
    // 「如实」是 R8 的另一半:落成空块也算丢了内容。
    expect((last.blocks[0] as { text: string }).text).toContain(
      "future_kind_from_a_newer_peer",
    );
  });

  it("reducePeerPullPage overlays the journal seq onto each frame", () => {
    let s = createPeerTranscript();
    s = reducePeerPullPage(s, [
      {
        seq: 1,
        params: {
          sessionId: 7,
          event: { kind: "user_message", text: "历史第一句" },
        },
      },
      {
        seq: 2,
        params: { sessionId: 7, event: { kind: "text_delta", text: "回复" } },
      },
    ]);
    expect(s.messages).toHaveLength(2);
    expect(s.messages[0]).toMatchObject({ role: "user", seq: 1 });
    expect(s.messages[1]).toMatchObject({ role: "assistant", seq: 2 });
    expect(s.cursor).toBe(2);
  });
});

// Given 一轮里夹着遥测帧（usage 每次 API call 一条、上下文窗口、运行状态）；
// When 归约；Then 它们不进正文 —— 一条都不该变成 raw 块铺在回答里。
//
// 从前这里全部落 default 分支 JSON.stringify 成 raw：28 个 kind 里有 16 个没写
// case，Peer Tab 的远端转录因此被遥测刷满。服务端那份归约器早就把这一档划成
// 「记而不显」，桌面端这一份没跟上。
describe("peer-transcript 遥测帧", () => {
  const noise: EventKind[] = [
    EventUsage,
    EventContextWindowUpdated,
    EventRuntimeStatus,
    EventPermissionModeChanged,
    EventOutputActivity,
    EventSteerConsumed,
    EventRetry,
    EventSubagentStarted,
    EventSubagentProgress,
    EventSubagentDone,
    EventSubagentModel,
  ];

  it("遥测帧一条都不进正文", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(s, frame(1, EventTextDelta, { text: "我看一下。" }));
    noise.forEach((kind, i) => {
      s = reducePeerEvent(
        s,
        frame(i + 2, kind, { usage: { promptTokens: 1 } }),
      );
    });
    s = reducePeerEvent(s, frame(99, EventTextDelta, { text: "看完了。" }));

    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].blocks).toEqual([
      { type: "text", text: "我看一下。看完了。" },
    ]);
  });

  // Given 对端送来一条它自己也读不懂的转录块；When 归约；Then 仍如实落 raw（R8）
  // —— 「不进正文」说的是遥测，不是「凡是没写 case 的都吞掉」。
  it("认不出的块仍如实呈现", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(
      s,
      frame(1, EventUnrecognizedBlock, {
        blockType: "future_block",
        data: { keep: true },
      }),
    );

    expect(s.messages).toHaveLength(1);
    // 同上:落点从自造的 `raw` 换成包认得的 `notice`,载荷这才真的画得出来。
    expect(s.messages[0].blocks[0]).toMatchObject({ type: "notice" });
    expect((s.messages[0].blocks[0] as { text: string }).text).toContain(
      "future_block",
    );
    expect(s.messages[0].blocks[0].raw).toMatchObject({
      blockType: "future_block",
      data: { keep: true },
    });
  });
});

// Given 从前一律落 raw(渲染成一行 debug 文字、载荷不可见)的那几种块;When 改用共享
// 归约器;Then Peer Tab 开始产出包认得的块类型。这是本次改动的可观察结果。
//
// 「包认得」不等于每一种都画一张卡:不带 canonical.actions 的 plan 块按 transcript-rows
// 的既有规则只喂 TaskProgressBar、不进转录行(与 agentre-server 同一行为)。真的渲染出来
// 那一段由 peer-panel.test.tsx 钉(compact 分隔线 + notice 载荷)。
describe("peer-transcript 改用共享归约器后新出现的块", () => {
  const landings: [EventKind, Record<string, unknown>, string][] = [
    [
      EventPlanUpdated,
      { text: "先读路由表", steps: [{ step: "读", status: "pending" }] },
      "plan",
    ],
    [
      EventCompactBoundary,
      { preTokens: 120000, trigger: "auto", at: 3 },
      "compact_boundary",
    ],
    [
      EventExecApprovalRequested,
      { id: "e1", commandText: "ls -al" },
      "exec_approval",
    ],
  ];

  it.each(landings)("%s 落成 %s 块", (kind, payload, blockType) => {
    let s = createPeerTranscript();
    s = reducePeerEvent(s, frame(1, kind, payload));

    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].blocks.map((b) => b.type)).toEqual([blockType]);
  });

  it("exec 审批的决议回填原卡,不新增一个块", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(
      s,
      frame(1, EventExecApprovalRequested, { id: "e1", commandText: "ls" }),
    );
    s = reducePeerEvent(
      s,
      frame(2, EventExecApprovalResolved, {
        id: "e1",
        status: "resolved",
        decision: "allow-once",
      }),
    );

    expect(s.messages[0].blocks).toHaveLength(1);
    expect(s.messages[0].blocks[0].execApproval).toMatchObject({
      id: "e1",
      status: "resolved",
      decision: "allow-once",
    });
  });

  it("error 落消息级 errorText,不再往正文里塞一个块", () => {
    // 从前这里追加一个 raw 块 + errorText 两处各记一遍。包在末行渲染 ErrorCard。
    let s = createPeerTranscript();
    s = reducePeerEvent(s, frame(1, EventTextDelta, { text: "好的。" }));
    s = reducePeerEvent(s, frame(2, "error", { message: "connection reset" }));

    expect(s.messages[0].errorText).toBe("connection reset");
    expect(s.messages[0].blocks.map((b) => b.type)).toEqual(["text"]);
  });
});

// Given 对端要我批一次工具 / 回答一个问题;When 归约;Then 那张卡**不进转录**,只进
// 待决策清单 —— 包里的交互卡按下去会调 TranscriptPorts,而桌面端顶层注入的是本机
// 会话的 Wails 绑定,拿远端 sessionId 去答本地会话是答错人。Peer Panel 自绘的卡片
// 走 peer 绑定,这条边界因此是故意的,不是漏渲染。
describe("peer-transcript 交互卡归 Peer Panel", () => {
  it("提问卡不进转录,只进待决策清单", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(
      s,
      frame(1, EventAskUserQuestion, {
        requestId: "q1",
        questions: [{ question: "继续?", header: "确认", options: [] }],
      }),
    );

    // 摘空之后不留一条没有正文的空气泡。
    expect(s.messages).toHaveLength(0);
    expect(s.decisions).toHaveLength(1);
    expect(s.decisions[0]).toMatchObject({ kind: "ask", requestId: "q1" });
  });

  it("授权卡不进转录,但同一轮的正文照常渲染", () => {
    let s = createPeerTranscript();
    s = reducePeerEvent(s, frame(1, EventTextDelta, { text: "我跑一下。" }));
    s = reducePeerEvent(
      s,
      frame(2, EventToolPermissionRequest, {
        requestId: "p1",
        toolName: "Bash",
        input: { command: "ls" },
      }),
    );

    expect(s.messages).toHaveLength(1);
    expect(s.messages[0].blocks.map((b) => b.type)).toEqual(["text"]);
    expect(s.decisions[0]).toMatchObject({
      kind: "permission",
      requestId: "p1",
      toolName: "Bash",
      input: { command: "ls" },
    });
  });
});
