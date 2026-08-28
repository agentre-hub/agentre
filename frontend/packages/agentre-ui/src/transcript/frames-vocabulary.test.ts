/**
 * 事件词表的**穷尽守卫**：`EventKind` 里的每一个 kind 在 `reduceFrames` 里都有一个
 * 明写的归宿，一个都不许掉进 `default`。
 *
 * ## 为什么要机械地过一遍词表
 *
 * `frames.test.ts` 是按归宿一条条钉的，读起来清楚，但它天然只覆盖作者当时想到的那些
 * kind。Go 那边新增一个事件、`event-kinds.gen.ts` 重新生成之后，实现里那句
 * `const _: never = kind` 会在**编译期**变红 —— 前提是有人跑 tsc。而这份守卫是运行期
 * 的第二道：把词表当数据遍历，落进 `default` 分支的 kind 会被点名。
 *
 * `default` 的产物是一张 `notice` 卡，上面印着整坨 JSON。它对**真·词表外**的判别值
 * （比本仓新的对端、坏帧）是正确的（R8：如实呈现，不吞掉）；对词表**内**的 kind 却是
 * 事故 —— agentre-server 上线时 usage 每次 API call 就来一条，正文里 JSON 比回答还多；
 * 桌面端 Peer Tab 则是把它们印成自造的 `raw` 块，最终渲染成一行
 * `(debug) unimplemented block type: raw`，载荷连看都看不到。两侧同一个根因。
 *
 * ## 这里为什么不接 relay / peer 的传输层
 *
 * agentre-server 另有一份 `relay-event-vocabulary.test.ts`，把真 Protobuf 帧灌进它的
 * `RelayClient`、再喂给这个归约器 —— 那一跳（oneof case 名 → kind 判别值）是**宿主的
 * 帧来源**，桌面端那一侧对应的是 `peer_svc` 的 Wails 事件。传输属于宿主，按
 * `AGENTS.md` 的跨宿主所有权规则留在各自仓库；进包的是它下游的这一段：拿到 kind 之后
 * 该落哪。
 */
import {
  EventAskUserQuestion,
  EventAskUserQuestionAnswered,
  EventCompactBoundary,
  EventContextWindowUpdated,
  EventDone,
  EventError,
  EventExecApprovalRequested,
  EventExecApprovalResolved,
  EventOutputActivity,
  EventPermissionModeChanged,
  EventPlanUpdated,
  EventRetry,
  EventRuntimeStatus,
  EventSteerConsumed,
  EventSubagentDone,
  EventSubagentModel,
  EventSubagentProgress,
  EventSubagentStarted,
  EventTextDelta,
  EventThinkingDelta,
  EventToolPermissionRequest,
  EventToolPermissionResolved,
  EventToolResult,
  EventToolUseEnd,
  EventToolUseStart,
  EventUnrecognizedBlock,
  EventUsage,
  EventUserMessage,
  type EventKind,
} from "../event-kinds.gen";
import { describe, expect, it } from "vitest";

import { reduceFrames, type TranscriptFrame } from "./frames";

const SID = 3;

/**
 * 每个 kind 的**归宿**，写成一张表而不是一串 if。
 *
 * `block` —— 落进正文的块类型；`silent` —— 认得但归宿不在正文（会话状态 / Composer /
 * 消息级字段）；`backfill` —— 回填先前那张卡，本身不新增块。
 *
 * 写成 `Record<EventKind, …>` 是这份守卫的要害：Go 新增一个 kind、wire 包重新生成之后，
 * **这张表在编译期就红**，逼一次「它落哪」的决定，而不是等线上多出一坨 JSON。
 */
type Landing =
  | { kind: "block"; blockType: string }
  | { kind: "silent" }
  | { kind: "backfill" };

const LANDINGS: Record<EventKind, Landing> = {
  [EventTextDelta]: { kind: "block", blockType: "text" },
  [EventThinkingDelta]: { kind: "block", blockType: "thinking" },
  [EventUserMessage]: { kind: "block", blockType: "text" },
  [EventToolUseStart]: { kind: "block", blockType: "tool_use" },
  [EventToolResult]: { kind: "block", blockType: "tool_result" },
  [EventAskUserQuestion]: { kind: "block", blockType: "ask_user_question" },
  [EventToolPermissionRequest]: {
    kind: "block",
    blockType: "tool_permission_request",
  },
  [EventExecApprovalRequested]: { kind: "block", blockType: "exec_approval" },
  [EventPlanUpdated]: { kind: "block", blockType: "plan" },
  [EventCompactBoundary]: { kind: "block", blockType: "compact_boundary" },
  [EventUnrecognizedBlock]: { kind: "block", blockType: "notice" },
  [EventAskUserQuestionAnswered]: { kind: "backfill" },
  [EventToolPermissionResolved]: { kind: "backfill" },
  [EventExecApprovalResolved]: { kind: "backfill" },
  // usage / error 是消息级：写进 token 列或 errorText，正文里不多出块。
  [EventUsage]: { kind: "silent" },
  [EventError]: { kind: "silent" },
  [EventDone]: { kind: "silent" },
  [EventContextWindowUpdated]: { kind: "silent" },
  [EventRuntimeStatus]: { kind: "silent" },
  [EventPermissionModeChanged]: { kind: "silent" },
  [EventSteerConsumed]: { kind: "silent" },
  [EventToolUseEnd]: { kind: "silent" },
  [EventRetry]: { kind: "silent" },
  [EventOutputActivity]: { kind: "silent" },
  [EventSubagentStarted]: { kind: "silent" },
  [EventSubagentProgress]: { kind: "silent" },
  [EventSubagentDone]: { kind: "silent" },
  [EventSubagentModel]: { kind: "silent" },
};

/** 最小载荷。只要够让那一档走到自己的分支，不追求真实。 */
const PAYLOADS: Partial<Record<EventKind, Record<string, unknown>>> = {
  [EventTextDelta]: { text: "hi" },
  [EventThinkingDelta]: { text: "hmm" },
  [EventUserMessage]: { text: "hello" },
  [EventToolUseStart]: { id: "t1", name: "Read", input: {} },
  [EventToolResult]: { toolCallId: "t1", content: "ok" },
  [EventAskUserQuestion]: { requestId: "q1", questions: [] },
  [EventToolPermissionRequest]: {
    requestId: "p1",
    toolName: "Bash",
    input: {},
  },
  [EventExecApprovalRequested]: { id: "e1", commandText: "ls" },
  [EventPlanUpdated]: { text: "plan" },
  [EventCompactBoundary]: { preTokens: 2, trigger: "auto" },
  [EventUnrecognizedBlock]: { blockType: "future_block", data: { keep: true } },
  [EventError]: { message: "boom" },
  [EventUsage]: { usage: { promptTokens: 1 } },
  [EventContextWindowUpdated]: { tokens: 10 },
  [EventRuntimeStatus]: { status: "compacting" },
  [EventPermissionModeChanged]: { mode: "plan" },
  [EventRetry]: { attempt: 1, max: 3 },
  [EventSubagentModel]: { toolCallId: "s1", model: "opus" },
};

const kinds = Object.keys(LANDINGS) as EventKind[];

function frame(kind: EventKind): TranscriptFrame {
  return { sessionId: SID, event: { kind, ...(PAYLOADS[kind] ?? {}) }, seq: 1 };
}

describe("EventKind 词表的归宿", () => {
  // Given wire 词表里的任意一个 kind；When 单独归约一帧；Then 它不许落进 default ——
  // default 印的是整坨 JSON，那是留给**词表外**判别值的如实呈现，不是词表内的归宿。
  it("词表里没有任何 kind 掉进 default 的 JSON notice", () => {
    const strays = kinds.filter((kind) => {
      const landing = LANDINGS[kind];
      const blocks = reduceFrames([frame(kind)], SID).flatMap((m) => m.blocks);
      // unrecognized_block 自己就落 notice（发送方读不懂并原样带过来），它与
      // default 的区别只在**是谁读不懂**，所以这一条按它自己的归宿判。
      if (landing.kind === "block" && landing.blockType === "notice")
        return false;
      return blocks.some((b) => b.type === "notice");
    });

    expect(strays).toEqual([]);
  });

  // Given 归宿表里写着某个 kind 落哪；When 归约；Then 实际落点逐条对上。
  it.each(kinds)("%s 落在归宿表写的位置", (kind) => {
    const landing = LANDINGS[kind];
    const messages = reduceFrames([frame(kind)], SID);
    const blockTypes = messages.flatMap((m) => m.blocks).map((b) => b.type);

    if (landing.kind === "block") {
      expect(blockTypes).toEqual([landing.blockType]);
      return;
    }
    // silent 与 backfill 都不产块。backfill 的分支在没有原卡时按「不凭空造卡」
    // 处理（见 frames.test.ts 的 ghost 用例），单独喂一帧同样是空。
    expect(blockTypes).toEqual([]);
  });

  // Given 一个真·词表外的判别值；When 归约；Then 落 notice 并把载荷原样带上（R8）。
  // 这一条与上面那条是一体两面：default 必须留着，但只许收词表外的东西。
  it("词表外的判别值仍如实落 notice", () => {
    const [msg] = reduceFrames(
      [
        {
          sessionId: SID,
          event: { kind: "kind_from_a_newer_daemon", payload: 42 },
          seq: 1,
        },
      ],
      SID,
    );

    expect(msg.blocks[0].type).toBe("notice");
    expect(msg.blocks[0].text).toContain("kind_from_a_newer_daemon");
    expect(msg.blocks[0].raw).toMatchObject({ payload: 42 });
  });
});
