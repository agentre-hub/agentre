// chat-streams-store 是跨路由长存的「流式状态/排队/已结束广播」单点。它由 ChatStreamsHost
// 在 App 顶层订阅 Wails 事件并写入，ChatPanel 通过 selector 读取。这里的测试只覆盖 store
// 自身的行为契约：不要 mount React。
//
// 关键约束（前后端共有）：
//   - 文字 delta 累在 liveDelta；遇到 tool_use 先把当时的 liveDelta 冻成一个 text block 推到
//     liveBlocks 尾部，再 push tool_use，liveDelta 清空。这样 [...persisted, ...liveBlocks,
//     trailing TextBlock(liveDelta)] 的渲染顺序就是真实流入顺序。
//   - tool_result 不 flush liveDelta（紧跟 tool_use，不应中间插字）。
//   - finishStream / failStream / closeStream 都会清空该 session 的 LiveStream，
//     并把 doneTick 自增 → 订阅者据此 reload 持久化消息。
import { beforeEach, describe, expect, it } from "vitest";

import {
  hasSessionStream,
  streamForMessage,
  useChatStreamsStore,
} from "../chat-streams-store";
import { useQueuedMessagesStore } from "../queued-messages-store";
import { useSessionStatusStore } from "../session-status-store";

import type { chat_svc, view } from "../../../wailsjs/go/models";

function resetStore() {
  useChatStreamsStore.setState({ streams: new Map() });
  useQueuedMessagesStore.getState().__reset();
  useSessionStatusStore.getState().__reset();
}

/** live 取绑在 assistantMessageId=1(baseStream 的默认值)上的那条流。 */
const live = (sessionId: number, messageId = 1) =>
  streamForMessage(useChatStreamsStore.getState(), sessionId, messageId);

const baseStream = (sessionId: number) => ({
  name: `chat:event:${sessionId}:1`,
  sessionId,
  assistantMessageId: 1,
  streamStartedAt: Date.now(),
});

describe("chat-streams-store", () => {
  beforeEach(() => {
    resetStore();
  });

  it("openStream initializes empty live state", () => {
    useChatStreamsStore.getState().openStream(baseStream(7));
    const s = live(7);
    expect(s).toBeTruthy();
    expect(s!.liveDelta).toBe("");
    expect(s!.liveThinking).toBe("");
    expect(s!.liveBlocks).toEqual([]);
  });

  it("patchLiveUsage atomically applies the context window carried by usage", () => {
    const { openStream, patchLiveUsage } = useChatStreamsStore.getState();
    openStream(baseStream(7));

    const usage = {
      messageId: 1,
      promptTokens: 1200,
      totalInputTokens: 3600,
      contextWindow: 258000,
    };
    patchLiveUsage(7, 1, usage);

    const s = live(7)!;
    expect(s.liveUsage).toEqual(usage);
    expect(s.liveContextWindow).toBe(258000);
  });

  it("Given two usage frames, When patched, Then completion accumulates and prompt is the latest", () => {
    const { openStream, patchLiveUsage } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    patchLiveUsage(7, 1, {
      promptTokens: 12000,
      completionTokens: 200,
      reasoningTokens: 10,
      totalInputTokens: 12000,
    });
    patchLiveUsage(7, 1, {
      promptTokens: 22000,
      completionTokens: 400,
      reasoningTokens: 5,
      totalInputTokens: 22000,
    });
    const s = live(7)!;
    expect(s.liveUsage?.promptTokens).toBe(22000);
    expect(s.turnCompletionTokens).toBe(600);
    expect(s.turnReasoningTokens).toBe(15);
  });

  it("Given the first text delta, When appended, Then firstTokenAt is recorded once", () => {
    const { openStream, appendLiveText } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveText(7, 1, "hello");
    const first = live(7)!.firstTokenAt;
    expect(first).toBeTruthy();
    appendLiveText(7, 1, " world");
    expect(live(7)!.firstTokenAt).toBe(first);
  });

  it("appendLiveText appends to liveDelta (not yet frozen)", () => {
    const { openStream, appendLiveText } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveText(7, 1, "hello ");
    appendLiveText(7, 1, "world");
    const s = live(7);
    expect(s!.liveDelta).toBe("hello world");
    expect(s!.liveBlocks).toHaveLength(0);
  });

  it("appendLiveToolUse freezes pending liveDelta into a text block first", () => {
    const { openStream, appendLiveText, appendLiveToolUse } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveText(7, 1, "let me check ");
    appendLiveToolUse(7, 1, {
      toolName: "read_file",
      toolUseId: "t1",
      toolInput: { path: "a" },
    });
    const s = live(7);
    expect(s!.liveDelta).toBe("");
    expect(s!.liveBlocks).toHaveLength(2);
    expect(s!.liveBlocks[0]).toMatchObject({
      type: "text",
      text: "let me check ",
    });
    expect(s!.liveBlocks[1]).toMatchObject({
      type: "tool_use",
      toolName: "read_file",
      toolUseId: "t1",
    });
  });

  it("appendLiveToolUse freezes pending liveThinking into a thinking block before text", () => {
    // 回归 guard:工具循环里第 2 轮的 thinking 必须按时间顺序冻进 liveBlocks,
    // 而不是留在 liveThinking 里被 renderer 抬到最顶(「思考完成过程都在最顶部叠加」)。
    const {
      openStream,
      appendLiveThinking,
      appendLiveText,
      appendLiveToolUse,
      appendLiveToolResult,
    } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    // round 1: thinking → text → tool_use
    appendLiveThinking(7, 1, "thought1 ");
    appendLiveText(7, 1, "text1 ");
    appendLiveToolUse(7, 1, {
      toolName: "Bash",
      toolUseId: "t1",
    });
    let s = live(7);
    expect(s!.liveThinking).toBe(""); // 已冻结
    expect(s!.liveBlocks.map((b) => b.type)).toEqual([
      "thinking",
      "text",
      "tool_use",
    ]);
    expect(s!.liveBlocks[0]).toMatchObject({
      type: "thinking",
      text: "thought1 ",
    });

    // round 2: 新一轮 thinking,在 tool_result 之后
    appendLiveToolResult(7, 1, { toolUseId: "t1", text: "ok" });
    appendLiveThinking(7, 1, "thought2 ");
    appendLiveText(7, 1, "text2 ");
    appendLiveToolUse(7, 1, {
      toolName: "Read",
      toolUseId: "t2",
    });
    s = live(7);
    expect(s!.liveThinking).toBe("");
    expect(s!.liveBlocks.map((b) => b.type)).toEqual([
      "thinking",
      "text",
      "tool_use",
      "tool_result",
      "thinking",
      "text",
      "tool_use",
    ]);
    // round 2 的 thinking 排在 round 1 的 tool_result 之后(时间顺序)。
    expect(s!.liveBlocks[4]).toMatchObject({
      type: "thinking",
      text: "thought2 ",
    });
  });

  it("appendLiveToolResult does NOT flush liveDelta", () => {
    // 设计上 tool_result 紧跟 tool_use 出现,中间不会有用户可见的文字增量。
    const { openStream, appendLiveText, appendLiveToolResult } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveText(7, 1, "x");
    appendLiveToolResult(7, 1, {
      toolUseId: "t1",
      text: "ok",
    });
    const s = live(7);
    expect(s!.liveDelta).toBe("x");
    expect(s!.liveBlocks).toHaveLength(1);
    expect(s!.liveBlocks[0]).toMatchObject({ type: "tool_result" });
  });

  it("appendLiveToolResult preserves toolResultMeta so task-progress can derive in real time", () => {
    // 回归 guard: turn 进行中 TaskCreate 的 tool_result 带 toolResultMeta.task.id,
    // store 必须把它透到 liveBlocks; 否则 chat-panel 的 deriveTaskProgress(messages,
    // liveBlocks) 在 turn 结束前永远拿不到真实 taskId, TaskCreate 不会实时入列表。
    const { openStream, appendLiveToolResult } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveToolResult(7, 1, {
      toolUseId: "toolu_A",
      text: "Task #1 created successfully: probe",
      toolResultMeta: { task: { id: "1", subject: "probe" } },
    });
    const s = live(7);
    expect(s!.liveBlocks).toHaveLength(1);
    expect(s!.liveBlocks[0]).toMatchObject({
      type: "tool_result",
      toolUseId: "toolu_A",
      toolResultMeta: { task: { id: "1", subject: "probe" } },
    });
  });

  it("appendLivePlanUpdate freezes pending text and upserts one live plan block", () => {
    const { openStream, appendLiveText, appendLivePlanUpdate } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveText(7, 1, "preface");
    const first = {
      kind: "plan.update",
      planUpdate: {
        text: "# Plan\n\n1. Inspect",
        steps: [{ step: "Inspect", status: "inProgress" }],
      },
    } as unknown as view.CanonicalDTO;
    appendLivePlanUpdate(7, 1, "# Plan\n\n1. Inspect", first);

    const second = {
      kind: "plan.update",
      planUpdate: {
        text: "# Plan\n\n1. Inspect\n2. Test",
        steps: [
          { step: "Inspect", status: "completed" },
          { step: "Test", status: "inProgress" },
        ],
      },
    } as unknown as view.CanonicalDTO;
    appendLivePlanUpdate(7, 1, "# Plan\n\n1. Inspect\n2. Test", second);

    const s = live(7)!;
    expect(s.liveDelta).toBe("");
    expect(s.liveBlocks).toHaveLength(2);
    expect(s.liveBlocks[0]).toMatchObject({ type: "text", text: "preface" });
    expect(s.liveBlocks[1]).toMatchObject({
      type: "plan",
      text: "# Plan\n\n1. Inspect\n2. Test",
      canonical: { kind: "plan.update" },
    });
  });

  it("appendLiveThinking accumulates separately", () => {
    const { openStream, appendLiveText, appendLiveThinking } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveThinking(7, 1, "think ");
    appendLiveText(7, 1, "answer");
    appendLiveThinking(7, 1, "more");
    const s = live(7);
    expect(s!.liveThinking).toBe("think more");
    expect(s!.liveDelta).toBe("answer");
    expect(s!.liveBlocks).toHaveLength(0);
  });

  it("appendLiveText / tool / tool_result on missing session is a no-op", () => {
    // 切走时 openStream 已被 closeStream 调用 → 后续晚到的事件不应崩
    const { appendLiveText, appendLiveToolUse } =
      useChatStreamsStore.getState();
    appendLiveText(99, 1, "ghost");
    appendLiveToolUse(99, 1, { toolName: "x" } as chat_svc.ChatBlock);
    expect(hasSessionStream(useChatStreamsStore.getState(), 99)).toBe(false);
  });

  it("finishStream closes the LiveStream, bumps doneTick and stores last done event", () => {
    const { openStream, finishStream } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    finishStream(7, 1, {
      kind: "done",
      message: { id: 100, sessionId: 7 } as chat_svc.ChatMessage,
    });
    expect(hasSessionStream(useChatStreamsStore.getState(), 7)).toBe(false);
    const status = useSessionStatusStore.getState().statuses.get(7);
    expect(status?.doneTick).toBe(1);
    expect(status?.lastDoneEvent?.kind).toBe("done");
  });

  it("finishStream tick increments monotonically per session", () => {
    const { openStream, finishStream } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    finishStream(7, 1, { kind: "done" });
    openStream(baseStream(7));
    finishStream(7, 1, { kind: "done" });
    expect(useSessionStatusStore.getState().statuses.get(7)?.doneTick).toBe(2);
  });

  it("finishStream moves never-consumed queued messages into dropped instead of silently clearing them", () => {
    const { openStream, finishStream } = useChatStreamsStore.getState();
    useQueuedMessagesStore
      .getState()
      .append(7, { id: "qid-1", text: "queued first", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(7, { id: "qid-2", text: "queued second", cancellable: false });

    openStream(baseStream(7));
    finishStream(7, 1, { kind: "done" });

    // 队列被清空(不再挂在 queuedBySession)
    expect(useQueuedMessagesStore.getState().queuedBySession.has(7)).toBe(
      false,
    );
    // 但内容被暂存进 dropped,没有静默丢失
    const dropped = useQueuedMessagesStore.getState().dropped;
    expect(dropped?.sessionId).toBe(7);
    expect(dropped?.items.map((q) => q.id)).toEqual(["qid-1", "qid-2"]);
  });

  it("finishStream without queued messages leaves dropped untouched (no spurious record)", () => {
    const { openStream, finishStream } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    finishStream(7, 1, { kind: "done" });
    expect(useQueuedMessagesStore.getState().dropped).toBeNull();
  });

  it("consumeSteer removes consumed queued IDs and retargets the live assistant", () => {
    const { openStream, appendLiveText, consumeSteer } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveText(7, 1, "before");
    useQueuedMessagesStore
      .getState()
      .append(7, { id: "qid-1", text: "next", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(7, { id: "qid-2", text: "later", cancellable: true });

    consumeSteer(7, 1, {
      kind: "steer_consumed",
      queuedIds: ["qid-1"],
      assistantMessage: { id: 22, sessionId: 7 } as chat_svc.ChatMessage,
    });

    const remaining = useQueuedMessagesStore.getState().queuedBySession.get(7);
    expect(remaining?.map((q) => q.id)).toEqual(["qid-2"]);
    // steer 换了 assistant 占位 → 流按新 messageId 重挂,旧 key 不再存在。
    expect(live(7, 1)).toBeNull();
    expect(live(7, 22)!.assistantMessageId).toBe(22);
    expect(live(7, 22)!.liveDelta).toBe("");
    const status = useSessionStatusStore.getState().statuses.get(7);
    expect(status?.doneTick).toBe(1);
    expect(status?.lastDoneEvent?.kind).toBe("steer_consumed");
  });

  it("setLiveRetry then clearLiveRetry resets liveRetry without disturbing other live state", () => {
    // 场景:CLI 发出 api_retry 帧 → setLiveRetry 写状态;紧接着重试成功收到的
    // 第一个 chunk/tool_use 等事件就会 clearLiveRetry —— 之前累积的 liveDelta
    // / liveThinking / liveBlocks 不能被一起清掉(那些是 turn 内的有效内容)。
    const {
      openStream,
      appendLiveText,
      appendLiveThinking,
      setLiveRetry,
      clearLiveRetry,
    } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveText(7, 1, "partial");
    appendLiveThinking(7, 1, "thought");
    setLiveRetry(7, 1, {
      attempt: 1,
      maxAttempts: 10,
      message: "HTTP 529 rate_limit",
      details: "≈0.6s 后重试",
      at: 1700000000000,
    });
    expect(live(7)!.liveRetry).not.toBeNull();

    clearLiveRetry(7, 1);
    const s = live(7)!;
    expect(s.liveRetry).toBeNull();
    expect(s.liveDelta).toBe("partial");
    expect(s.liveThinking).toBe("thought");
  });

  it("clearLiveRetry on missing session or already-null liveRetry is a referential no-op", () => {
    // 引用一致性很重要:zustand selector(s) => s.streams 在引用不变时不会触发
    // ChatStreamsHost / ChatPanel 重渲染。每个 chunk 事件都会顺手清一次,如果
    // null → null 也换 Map 引用,会把每一帧都变成 O(订阅者) 的 re-render 风暴。
    const { clearLiveRetry } = useChatStreamsStore.getState();

    const before1 = useChatStreamsStore.getState().streams;
    clearLiveRetry(99, 1);
    expect(useChatStreamsStore.getState().streams).toBe(before1);

    useChatStreamsStore.getState().openStream(baseStream(7));
    const before2 = useChatStreamsStore.getState().streams;
    clearLiveRetry(7, 1); // liveRetry 本就是 null
    expect(useChatStreamsStore.getState().streams).toBe(before2);
  });

  // ── tool_permission_request live 路径必须把后端 ev.canonical 一并落到 block ──
  // 回归 bug: chat-streams-host 漏传 canonical → block 只有 toolPermission sidecar、
  // 无 canonical → CanonicalToolRouter fallback 到 RawToolCard → 渲染成空标题
  // "tool" 卡 + 简化 2 按钮 overlay,而不是 ToolPermissionCard(三按钮 + 可展开)。
  it("appendLiveToolPermissionRequest stores canonical onto the live block", () => {
    const { openStream, appendLiveToolPermissionRequest } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    const payload = {
      requestId: "req-1",
      toolName: "Bash",
      toolInput: { command: "ls" },
    } as chat_svc.ChatBlockToolPermission;
    const canonical = {
      kind: "tool.permission",
      toolPermission: {
        requestId: "req-1",
        toolName: "Bash",
        toolInput: { command: "ls" },
        resolved: false,
        allowed: false,
        alwaysAllow: false,
      },
    } as unknown as view.CanonicalDTO;
    appendLiveToolPermissionRequest(7, 1, payload, canonical);
    const s = live(7)!;
    expect(s.liveBlocks).toHaveLength(1);
    expect(s.liveBlocks[0]).toMatchObject({
      type: "tool_permission_request",
      toolPermission: { requestId: "req-1", toolName: "Bash" },
      canonical: { kind: "tool.permission" },
    });
  });

  it("markToolPermissionResolved keeps canonical and reflects the resolved decision", () => {
    // resolved 路径:append 时已写 canonical,resolve 后 store 内部要把
    // canonical.toolPermission.{resolved,allowed,alwaysAllow} 同步推进 ——
    // ToolPermissionCard 读 canonical 为 truth,sidecar 仅做兜底。
    const {
      openStream,
      appendLiveToolPermissionRequest,
      markToolPermissionResolved,
    } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    const initial = {
      requestId: "req-2",
      toolName: "Write",
      toolInput: { file_path: "/tmp/x" },
    } as chat_svc.ChatBlockToolPermission;
    const canonical = {
      kind: "tool.permission",
      toolPermission: {
        requestId: "req-2",
        toolName: "Write",
        toolInput: { file_path: "/tmp/x" },
        resolved: false,
        allowed: false,
        alwaysAllow: false,
      },
    } as unknown as view.CanonicalDTO;
    appendLiveToolPermissionRequest(7, 1, initial, canonical);

    markToolPermissionResolved(7, 1, {
      ...initial,
      resolved: true,
      allowed: true,
      alwaysAllow: true,
    } as chat_svc.ChatBlockToolPermission);

    const block = live(7)!.liveBlocks[0];
    expect(block.toolPermission).toMatchObject({
      resolved: true,
      allowed: true,
      alwaysAllow: true,
    });
    expect(block.canonical).toMatchObject({
      kind: "tool.permission",
      toolPermission: {
        resolved: true,
        allowed: true,
        alwaysAllow: true,
      },
    });
  });

  // ── tool_approval(内置写工具审批)live 路径 —— 镜像 tool permission 两式 ──
  it("appendLiveToolApproval pushes a pending tool_approval live block", () => {
    const { openStream, appendLiveToolApproval } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveToolApproval(7, 1, {
      toolKey: "org",
      requestId: "org-1",
      toolName: "org_create_department",
      toolInput: { name: "研发部" },
      status: "pending",
    });
    const s = live(7)!;
    expect(s.liveBlocks).toHaveLength(1);
    expect(s.liveBlocks[0]).toMatchObject({
      type: "tool_approval",
      toolApproval: {
        toolKey: "org",
        requestId: "org-1",
        toolName: "org_create_department",
        status: "pending",
      },
    });
  });

  it("markToolApprovalResolved updates status/result by requestId", () => {
    const { openStream, appendLiveToolApproval, markToolApprovalResolved } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveToolApproval(7, 1, {
      toolKey: "org",
      requestId: "org-2",
      toolName: "org_delete_agent",
      toolInput: { id: 9 },
      status: "pending",
    });
    markToolApprovalResolved(7, 1, {
      toolKey: "org",
      requestId: "org-2",
      toolName: "org_delete_agent",
      status: "approved",
      result: "已删除 Agent #9",
    });
    const block = live(7)!.liveBlocks[0];
    expect(block.toolApproval).toMatchObject({
      requestId: "org-2",
      status: "approved",
      result: "已删除 Agent #9",
    });
  });

  it("markToolApprovalResolved is a no-op for an unknown requestId", () => {
    const { openStream, appendLiveToolApproval, markToolApprovalResolved } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveToolApproval(7, 1, {
      toolKey: "org",
      requestId: "org-3",
      toolName: "org_update_agent",
      status: "pending",
    });
    const before = useChatStreamsStore.getState().streams;
    markToolApprovalResolved(7, 1, {
      toolKey: "org",
      requestId: "does-not-exist",
      toolName: "org_update_agent",
      status: "denied",
    });
    // 未知 requestId 既不改块也不重建 Map(referential no-op)。
    expect(useChatStreamsStore.getState().streams).toBe(before);
    const block = live(7)!.liveBlocks[0];
    expect(block.toolApproval).toMatchObject({ status: "pending" });
  });

  // ── OpenClaw exec approval live path ──
  it("Given pending text, When an exec approval arrives, Then text is flushed before one approval block", () => {
    const { openStream, appendLiveText, appendLiveExecApproval } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveText(7, 1, "Need approval first.");

    appendLiveExecApproval(7, 1, {
      id: "exec-1",
      commandText: "pwd",
      allowedDecisions: ["allow-once", "deny"],
      status: "pending",
    });

    expect(live(7)?.liveDelta).toBe("");
    expect(live(7)?.liveBlocks).toEqual([
      expect.objectContaining({ type: "text", text: "Need approval first." }),
      expect.objectContaining({
        type: "exec_approval",
        execApproval: expect.objectContaining({
          id: "exec-1",
          status: "pending",
        }),
      }),
    ]);
  });

  it("Given a pending exec approval, When a terminal update arrives, Then it is merged by id without creating a completion block", () => {
    const { openStream, appendLiveExecApproval, markExecApprovalResolved } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveExecApproval(7, 1, {
      id: "exec-2",
      commandText: "false",
      allowedDecisions: ["deny"],
      status: "pending",
    });

    markExecApprovalResolved(7, 1, {
      id: "exec-2",
      commandText: "false",
      status: "resolved",
      decision: "deny",
      resolvedBy: "operator-device-2",
    });

    expect(live(7)?.liveBlocks).toHaveLength(1);
    expect(live(7)?.liveBlocks[0]).toMatchObject({
      type: "exec_approval",
      execApproval: {
        id: "exec-2",
        status: "resolved",
        decision: "deny",
        resolvedBy: "operator-device-2",
      },
    });
  });

  it("Given no matching exec approval, When a terminal update arrives, Then store identity is unchanged", () => {
    const { openStream, markExecApprovalResolved } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    const before = useChatStreamsStore.getState().streams;

    markExecApprovalResolved(7, 1, {
      id: "missing",
      commandText: "",
      status: "expired",
    });

    expect(useChatStreamsStore.getState().streams).toBe(before);
  });

  // R4: subagent_model 事件只带 model 一个字段(不复用整份 Subagent 快照),调用方
  // 传入的 meta 也必须只有 model 一个 key —— 断言 mergeSubagentMeta 的浅合并在这种
  // 输入下确实不清空已累计的 status/toolUses/totalTokens。
  it("mergeSubagentMeta with a model-only patch does not clear existing progress/status fields", () => {
    const { openStream, appendLiveToolUse, mergeSubagentMeta } =
      useChatStreamsStore.getState();
    openStream(baseStream(7));
    appendLiveToolUse(7, 1, {
      toolUseId: "toolu_agent",
      toolName: "Agent",
      subagent: {
        status: "running",
        toolUses: 3,
        totalTokens: 500,
      } as chat_svc.ChatBlockSubagent,
    });
    mergeSubagentMeta(7, 1, "toolu_agent", {
      model: "claude-haiku-4-5-20251001",
    } as chat_svc.ChatBlockSubagent);
    const block = live(7)!.liveBlocks.find(
      (b) => b.toolUseId === "toolu_agent",
    );
    expect(block!.subagent).toMatchObject({
      status: "running",
      toolUses: 3,
      totalTokens: 500,
      model: "claude-haiku-4-5-20251001",
    });
  });

  it("mergeSubagentMeta silently no-ops when the toolUseId has no matching block", () => {
    const { openStream, mergeSubagentMeta } = useChatStreamsStore.getState();
    openStream(baseStream(7));
    const before = useChatStreamsStore.getState().streams;
    mergeSubagentMeta(7, 1, "toolu_missing", {
      model: "claude-haiku-4-5-20251001",
    } as chat_svc.ChatBlockSubagent);
    expect(useChatStreamsStore.getState().streams).toBe(before);
  });
});
