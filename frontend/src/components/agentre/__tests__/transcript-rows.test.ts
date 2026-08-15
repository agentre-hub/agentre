import { describe, expect, it } from "vitest";

import {
  applyLiveTranscriptRows,
  buildRenderItems,
  buildSourceByMessageId,
  buildSettledTranscriptRows,
  buildTranscriptRows,
  estimateRowSize,
  estimateRowSizeWithSpacing,
  isLastRowOfMessage,
  ROW_END_PADDING_DELTA,
  ROW_MID_PADDING_DELTA,
  summarizeActivity,
  type TranscriptRow,
  type TranscriptRowItem,
  type VisibleActivityItem,
  type VisibleRenderItem,
} from "@/components/agentre/transcript-rows";
import type {
  TranscriptBlock,
  TranscriptMessage,
} from "@agentre-ai/agentre-ui";
import type { LocalCommandEntry } from "@/stores/local-commands-store";

// buildRenderItems 是 renderMessageBlocks 状态机的纯函数抽取。这些单测把配对 /
// 合并 / skip / FIFO / 归集 / 合成顺序 / uiStateKey 字面量逐一钉死 —— 行级虚拟化
// 重构(把每个 RenderItem 拆成独立虚拟行)的逻辑根基全在这里。

function toolUse(
  toolUseId: string | undefined,
  toolName = "Bash",
  extra: Partial<TranscriptBlock> = {},
): TranscriptBlock {
  return {
    toolInput: { command: "echo hi" },
    toolName,
    toolUseId,
    type: "tool_use",
    ...extra,
  } as TranscriptBlock;
}

function toolResult(
  toolUseId: string | undefined,
  text = "ok",
  extra: Partial<TranscriptBlock> = {},
): TranscriptBlock {
  return { text, toolUseId, type: "tool_result", ...extra } as TranscriptBlock;
}

function text(t: string): TranscriptBlock {
  return { type: "text", text: t } as TranscriptBlock;
}

describe("buildRenderItems", () => {
  it("合并连续 text block,并把 tool_use/tool_result 按 toolUseId 配对成单个 tool item", () => {
    const items = buildRenderItems({
      messageId: 5,
      blocks: [
        text("Hello "),
        text("world"),
        toolUse("toolu-1"),
        toolResult("toolu-1", "paired"),
      ],
    });

    expect(items).toHaveLength(2);
    expect(items[0]).toMatchObject({ type: "text", text: "Hello world" });
    // 落单的一次调用是只有一步的活动项(壳由渲染层按「单条不成组」省掉)。
    const paired = activityAt(items, 1).steps[0];
    expect(paired).toMatchObject({
      type: "tool",
      toolBlock: { toolUseId: "toolu-1" },
      resultBlock: { text: "paired" },
    });
    // uiStateKey 字面量:格式 message:${id}:${type}:${identity},identity 优先 toolUseId。
    // TranscriptUIStateContext 里所有已展开卡片的状态都挂在这个键上,字节级不能漂。
    expect(paired.uiStateKey).toBe("message:5:tool:tool:toolu-1");
  });

  it("tool_use 在 persisted blocks、tool_result 在 liveBlocks 时仍配对到同一 item", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [toolUse("toolu-x")],
      liveBlocks: [toolResult("toolu-x", "late result")],
    });

    expect(items).toHaveLength(1);
    expect(activityAt(items, 0).steps[0]).toMatchObject({
      type: "tool",
      toolBlock: { toolUseId: "toolu-x" },
      resultBlock: { text: "late result" },
    });
  });

  it("匿名 tool(无 toolUseId)按 LIFO 配对,孤儿 tool_result 直接丢弃", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        toolUse(undefined, "Bash"),
        toolUse(undefined, "Read"),
        toolResult(undefined, "lifo-paired"),
        toolResult("toolu-orphan", "orphan"),
      ],
    });

    // 两个连续工具折进一个活动块,配对结果仍挂在各自的步骤上。
    expect(items).toHaveLength(1);
    const steps = activityAt(items, 0).steps;
    expect(steps).toHaveLength(2);
    // LIFO:匿名 result 配给最后一个匿名 tool_use,第一个保持未配对。
    expect(steps[0].type === "tool" && steps[0].resultBlock).toBeUndefined();
    expect(steps[1]).toMatchObject({
      type: "tool",
      toolBlock: { toolName: "Read" },
      resultBlock: { text: "lifo-paired" },
    });
    // 孤儿 result 不产生幽灵 tool 卡。
    expect(
      steps.some(
        (step) => step.type === "tool" && step.resultBlock?.text === "orphan",
      ),
    ).toBe(false);
  });

  it("AskUserQuestion / ExitPlanMode 的 tool_use 与对应 tool_result 双双跳过", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        toolUse("toolu-ask", "AskUserQuestion"),
        toolResult("toolu-ask", "answer"),
        toolUse("toolu-plan", "ExitPlanMode"),
        toolResult("toolu-plan", "approved"),
      ],
    });

    expect(items).toHaveLength(0);
  });

  it("resolved+allowed 审批被后续同名 tool_use FIFO 消费;denied 保留为独立卡", () => {
    const allowedPerm = {
      type: "tool_permission_request",
      toolPermission: {
        allowed: true,
        requestId: "req-allowed",
        resolved: true,
        toolName: "Bash",
      },
    } as unknown as TranscriptBlock;
    const deniedPerm = {
      type: "tool_permission_request",
      toolPermission: {
        allowed: false,
        requestId: "req-denied",
        resolved: true,
        toolName: "Bash",
      },
    } as unknown as TranscriptBlock;

    const items = buildRenderItems({
      messageId: 5,
      blocks: [allowedPerm, deniedPerm, toolUse("toolu-9", "Bash")],
    });

    expect(items).toHaveLength(2);
    expect(items[0]).toMatchObject({
      type: "tool_permission_request",
      block: { toolPermission: { requestId: "req-denied" } },
    });
    expect(items[0].uiStateKey).toBe(
      "message:5:permission:permission:req-denied",
    );
    // 被消费掉的是那条 allowed 审批：它不再单独成项，工具本身照常进活动块。
    // （审批信息由工具块上的 toolPermission 承载，不再往 RenderItem 上挂一份。）
    expect(activityAt(items, 1).steps[0]).toMatchObject({
      type: "tool",
      toolBlock: { toolName: "Bash" },
    });
  });

  it("tool_approval block 产出一个 tool_approval 渲染项", () => {
    const approvalBlock = {
      type: "tool_approval",
      toolApproval: {
        toolKey: "org",
        requestId: "org-1",
        toolName: "org_create_department",
        toolInput: { name: "研发部" },
        status: "pending",
      },
    } as unknown as TranscriptBlock;

    const items = buildRenderItems({
      messageId: 8,
      blocks: [approvalBlock],
    });

    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({
      type: "tool_approval",
      block: { toolApproval: { requestId: "org-1", status: "pending" } },
    });
  });

  it("notice block 产出一个 notice 渲染项（文本原样透传）", () => {
    const noticeBlock = {
      type: "notice",
      level: "info",
      text: "provider notice",
    } as unknown as TranscriptBlock;

    const items = buildRenderItems({
      messageId: 8,
      blocks: [noticeBlock],
    });

    expect(items).toHaveLength(1);
    expect(items[0].type).toBe("notice");
    const block = (items[0] as { block: TranscriptBlock }).block;
    expect(block.text).toBe("provider notice");
  });

  it("OpenClaw exec_approval block keeps its own lifecycle and stable approval identity", () => {
    const items = buildRenderItems({
      messageId: 9,
      blocks: [
        {
          type: "exec_approval",
          execApproval: {
            id: "exec-approval-1",
            commandText: "git status --short",
            allowedDecisions: ["allow-once", "deny"],
            status: "pending",
          },
        } as unknown as TranscriptBlock,
      ],
    });

    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({
      type: "exec_approval",
      block: {
        execApproval: { id: "exec-approval-1", status: "pending" },
      },
      uiStateKey: "message:9:exec_approval:exec-approval:exec-approval-1",
    });
  });

  it("agent.spawn 归集 parentToolUseId 子块到 childBlocks,子块不再上顶层", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        toolUse("toolu-parent", "Agent", {
          canonical: { kind: "agent.spawn" },
        } as unknown as Partial<TranscriptBlock>),
        toolUse("toolu-child", "Bash", { parentToolUseId: "toolu-parent" }),
        toolResult("toolu-child", "hello", { parentToolUseId: "toolu-parent" }),
        toolResult("toolu-parent", "Raw output"),
      ],
    });

    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({
      type: "tool",
      toolBlock: { toolUseId: "toolu-parent" },
      resultBlock: { text: "Raw output" },
    });
    const children = items[0].type === "tool" ? items[0].childBlocks : null;
    expect(children?.all).toHaveLength(2);
    expect(children?.byRun.size).toBe(0);
  });

  it("Given interleaved child blocks with reused call IDs, When rows are built, Then children group by parent and run while missing run IDs remain available to the parent", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        toolUse("toolu-parent", "subagent", {
          canonical: {
            kind: "agent.spawn",
            agentSpawn: { mode: "parallel", runs: [] },
          },
        } as unknown as Partial<TranscriptBlock>),
        toolUse("shared", "Read", {
          parentToolUseId: "toolu-parent",
          subagentRunId: "run-a",
        }),
        toolUse("shared", "Bash", {
          parentToolUseId: "toolu-parent",
          subagentRunId: "run-b",
        }),
        toolResult("shared", "read done", {
          parentToolUseId: "toolu-parent",
          subagentRunId: "run-a",
        }),
        toolUse("unknown", "Glob", {
          parentToolUseId: "toolu-parent",
        }),
      ],
    });

    expect(items).toHaveLength(1);
    const children = items[0].type === "tool" ? items[0].childBlocks : null;
    expect(children?.byRun.get("run-a")).toMatchObject([
      { type: "tool_use", toolName: "Read" },
      { type: "tool_result", text: "read done" },
    ]);
    expect(children?.byRun.get("run-b")).toMatchObject([
      { type: "tool_use", toolName: "Bash" },
    ]);
    expect(children?.all.map((block) => block.toolName ?? block.text)).toEqual([
      "Read",
      "Bash",
      "read done",
      "Glob",
    ]);
  });

  it("合成顺序:persisted → liveBlocks(含已冻结 thinking) → liveThinking → liveTail;thinking 的 streaming 只看 liveTail", () => {
    // case A:liveBlocks 已有 tool → 未冻结的 liveThinking 排在其后(时间顺序),
    // 且 thinking 的 streaming 为 false(liveTail 有 text,本轮思考已结束)。
    const withTool = buildRenderItems({
      messageId: 1,
      blocks: [text("done part")],
      liveThinking: "reasoning…",
      liveTail: " tail",
      liveBlocks: [toolUse("toolu-live")],
    });
    // 已结束的思考(streaming=false)与它前面的工具是同一段活动,折进一个活动块;
    // 段内顺序仍是「工具 → 思考」。
    expect(withTool.map((item) => item.type)).toEqual([
      "text",
      "activity",
      "text",
    ]);
    const withToolSteps = activityAt(withTool, 1).steps;
    expect(withToolSteps.map((step) => step.type)).toEqual([
      "tool",
      "thinking",
    ]);
    expect(withToolSteps[1]).toMatchObject({
      type: "thinking",
      streaming: false,
    });

    // case B:纯思考阶段(liveBlocks 空、liveTail 空)→ 仍排在最前,streaming=true。
    const thinkingOnly = buildRenderItems({
      messageId: 1,
      liveThinking: "reasoning…",
      liveThinkingStartedAt: 1234,
    });
    expect(thinkingOnly).toHaveLength(1);
    expect(thinkingOnly[0]).toMatchObject({
      type: "thinking",
      streaming: true,
      startedAt: 1234,
    });

    // case C:liveBlocks 里有前一轮工具、当前轮思考还没出 text → streaming 仍为 true
    // (过去用 liveBlocks.length===0 判定,会把「第 2 轮思考」误标成已结束)。
    const round2Thinking = buildRenderItems({
      messageId: 1,
      liveThinking: "round2…",
      liveBlocks: [
        toolUse("toolu-1"),
        { type: "tool_result", toolUseId: "toolu-1" } as TranscriptBlock,
      ],
    });
    expect(round2Thinking.map((item) => item.type)).toEqual([
      "activity",
      "thinking",
    ]);
    expect(round2Thinking[1]).toMatchObject({
      type: "thinking",
      streaming: true,
    });
  });

  // 回归 guard:流式思考不进组,于是它会短暂地排在活动块**后面**;渲染层据
  // 「是不是消息末行」判这一组还在不在跑,行模型不标出「后面那行只是临时的」,
  // 思考一开始流整组就被当成已落定收起、思考结束又展开(来回抖动)。
  it("末尾是仍在流的思考时,前一个活动块标 growing —— 那段思考一落定就并回来", () => {
    const growing = buildRenderItems({
      messageId: 1,
      liveThinking: "round2…",
      liveBlocks: [toolUse("toolu-1"), toolUse("toolu-2")],
    });

    expect(growing.map((item) => item.type)).toEqual(["activity", "thinking"]);
    expect(activityAt(growing, 0).growing).toBe(true);

    // 思考已落定(liveTail 有 text)→ 它并回块里,末尾那行不再是临时的,不标。
    const settled = buildRenderItems({
      messageId: 1,
      liveThinking: "round2…",
      liveTail: " tail",
      liveBlocks: [toolUse("toolu-1"), toolUse("toolu-2")],
    });

    expect(activityAt(settled, 0).growing).toBeUndefined();
  });

  it("工具循环里后一轮 thinking 穿插在 tool_result 之后,不再全堆最顶", () => {
    const items = buildRenderItems({
      messageId: 1,
      // liveBlocks 已含 round1 冻结段(thinking/text/tool_use/tool_result)
      // + round2 冻结段(thinking/text/tool_use)。
      liveBlocks: [
        { type: "thinking", text: "thought1" } as TranscriptBlock,
        { type: "text", text: "text1" } as TranscriptBlock,
        toolUse("toolu-1"),
        {
          type: "tool_result",
          toolUseId: "toolu-1",
          text: "r1",
        } as TranscriptBlock,
        { type: "thinking", text: "thought2" } as TranscriptBlock,
        { type: "text", text: "text2" } as TranscriptBlock,
        toolUse("toolu-2"),
      ],
    });
    // 活动块摊平成它的步骤 —— 聚合只改变「谁和谁同处一行」,不改变时间顺序。
    const label = (item: { type: string } & Record<string, unknown>): string =>
      item.type === "thinking"
        ? `thinking:${(item as unknown as { block: TranscriptBlock }).block.text}`
        : item.type === "tool"
          ? `tool:${(item as unknown as { toolBlock?: TranscriptBlock }).toolBlock?.toolName}`
          : item.type === "text"
            ? `text:${(item as unknown as { text: string }).text}`
            : item.type;
    const flat = items.flatMap((item) =>
      item.type === "activity" ? item.steps.map(label) : label(item),
    );
    // round1 思考 → text1 → tool1 → round2 思考 → text2 → tool2(时间顺序)
    expect(flat).toEqual([
      "thinking:thought1",
      "text:text1",
      "tool:Bash",
      "thinking:thought2",
      "text:text2",
      "tool:Bash",
    ]);
  });

  it("liveTail 与前面已冻结的 text 段合并为同一 item 并整体标记 streaming", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [text("abc")],
      liveTail: "def",
    });

    expect(items).toHaveLength(1);
    expect(items[0]).toMatchObject({
      type: "text",
      text: "abcdef",
      streaming: true,
    });
  });

  it("无身份的 item(如 thinking)uiStateKey 回退到 visible 下标", () => {
    const items = buildRenderItems({
      messageId: 5,
      blocks: [
        { type: "thinking", text: "chain" } as TranscriptBlock,
        text("说完了"),
      ],
    });

    expect(items).toHaveLength(2);
    // 已完成的思考进活动块,块内那一步的 key 仍是它单独成行时的字节形态。
    expect(activityAt(items, 0).steps[0].uiStateKey).toBe(
      "message:5:thinking:0",
    );
  });

  it("plan.update 只有 actionable(带 actions)才渲染,纯进度块丢弃", () => {
    const actionable = {
      type: "plan",
      canonical: {
        kind: "plan.update",
        planUpdate: { actions: [{ kind: "approve" }], steps: [], text: "" },
      },
    } as unknown as TranscriptBlock;
    const progressOnly = {
      type: "plan",
      canonical: {
        kind: "plan.update",
        planUpdate: { actions: [], steps: [{ title: "step" }], text: "" },
      },
    } as unknown as TranscriptBlock;

    const items = buildRenderItems({
      messageId: 1,
      blocks: [actionable, progressOnly],
    });

    expect(items).toHaveLength(1);
    expect(items[0].type).toBe("plan");
  });

  it("ask_user_question / compact_boundary / unknown / 空 text 的入列行为", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        text(""),
        { type: "ask_user_question" } as TranscriptBlock,
        { type: "compact_boundary" } as TranscriptBlock,
        { type: "mystery" } as TranscriptBlock,
      ],
    });

    expect(items.map((item) => item.type)).toEqual([
      "tool",
      "compact_boundary",
      "unknown",
    ]);
  });
});

describe("buildTranscriptRows source device (R17)", () => {
  it("attaches a source device to non-local user messages only", () => {
    const { rows } = buildTranscriptRows({
      displayMessages: [
        message(1, "user", [text("hi from another device")]),
        message(2, "assistant", [text("reply")]),
      ],
      autonomousIds: new Set(),
      sourceByMessageId: new Map([[1, "iPhone"]]),
    });
    expect(rows.find((r) => r.messageId === 1)?.sourceDevice).toBe("iPhone");
    expect(rows.find((r) => r.messageId === 2)?.sourceDevice).toBeUndefined();
  });

  it("leaves local user messages without a source identifier (single-client zero change)", () => {
    const { rows } = buildTranscriptRows({
      displayMessages: [message(1, "user", [text("hi")])],
      autonomousIds: new Set(),
    });
    expect(rows[0].sourceDevice).toBeUndefined();
  });

  it("never attaches a source to non-user roles", () => {
    const { rows } = buildTranscriptRows({
      displayMessages: [message(1, "assistant", [text("hi")])],
      autonomousIds: new Set(),
      sourceByMessageId: new Map([[1, "iPhone"]]),
    });
    expect(rows[0].sourceDevice).toBeUndefined();
  });

  // 连续 text block 会被 buildRenderItems 合并成一条 item,所以要真的产生多行,
  // 必须混入一个 tool_use —— 否则「每一行」这句话根本没被检验过。
  it("propagates the source across every row of the message", () => {
    const { rows } = buildTranscriptRows({
      displayMessages: [
        message(1, "user", [text("a"), toolUse("toolu-1"), text("b")]),
      ],
      autonomousIds: new Set(),
      sourceByMessageId: new Map([[1, "iPhone"]]),
    });
    expect(rows.length).toBeGreaterThan(1);
    for (const row of rows) {
      expect(row.sourceDevice).toBe("iPhone");
    }
  });
});

describe("buildSourceByMessageId (R17 caller side)", () => {
  const foreign = (
    id: number,
    sourceDevice: string,
    sourceDeviceName?: string,
  ) =>
    ({
      ...message(id, "user", [text("x")]),
      sourceDevice,
      sourceDeviceName,
    }) as TranscriptMessage;

  it("maps a foreign user message to its device name", () => {
    const out = buildSourceByMessageId(
      [foreign(1, "sha256:other", "iPhone")],
      "sha256:self",
    );
    expect(out.get(1)).toBe("iPhone");
  });

  it("falls back to the fingerprint when no device name is available", () => {
    const out = buildSourceByMessageId(
      [foreign(2, "sha256:other")],
      "sha256:self",
    );
    expect(out.get(2)).toBe("sha256:other");
  });

  it("never maps this device's own messages (single-client zero change)", () => {
    const out = buildSourceByMessageId(
      [foreign(3, "sha256:self")],
      "sha256:self",
    );
    expect(out.size).toBe(0);
  });

  it("skips messages without a source and non-user roles", () => {
    const out = buildSourceByMessageId(
      [
        message(4, "user", [text("no source")]),
        {
          ...message(5, "assistant", [text("assistant")]),
          sourceDevice: "sha256:other",
        } as TranscriptMessage,
      ],
      "sha256:self",
    );
    expect(out.size).toBe(0);
  });

  it("returns an empty map while the local fingerprint is unresolved", () => {
    const out = buildSourceByMessageId(
      [foreign(6, "sha256:other", "iPhone")],
      undefined,
    );
    expect(out.size).toBe(0);
  });
});

function message(
  id: number,
  role: "user" | "assistant",
  blocks: TranscriptBlock[],
): TranscriptMessage {
  return {
    blocks,
    completionTokens: 0,
    createtime: 0,
    durationMs: 0,
    errorText: "",
    id,
    model: "",
    promptTokens: 0,
    role,
    seq: id,
    sessionId: 1,
  } as TranscriptMessage;
}

describe("buildTranscriptRows", () => {
  it("把 displayMessages 平铺成行:每个 RenderItem 一行,首/末行标志与 messageId 正确", () => {
    const { rows, firstRowIndexByMessageId, rowIndexByKey } =
      buildTranscriptRows({
        displayMessages: [
          message(1, "user", [text("hi")]),
          message(2, "assistant", [
            text("reply"),
            toolUse("toolu-1"),
            toolResult("toolu-1"),
            toolUse("toolu-2"),
          ]),
        ],
        autonomousIds: new Set(),
      });

    expect(
      rows.map((r) => [
        r.messageId,
        r.item.type,
        r.isFirstOfMessage,
        r.isLastOfMessage,
      ]),
    ).toEqual([
      [1, "text", true, true],
      [2, "text", true, false],
      // 连续两次工具调用折成一个活动块 = 一个渲染项 = 一个虚拟行。
      [2, "activity", false, true],
    ]);
    expect(firstRowIndexByMessageId.get(1)).toBe(0);
    expect(firstRowIndexByMessageId.get(2)).toBe(1);
    for (const [idx, row] of rows.entries()) {
      expect(rowIndexByKey.get(row.key)).toBe(idx);
    }
    // 行 key 全局唯一。
    expect(new Set(rows.map((r) => r.key)).size).toBe(rows.length);
  });

  it("autonomous 标志只落在自主续轮消息的行上", () => {
    const { rows } = buildTranscriptRows({
      displayMessages: [
        message(1, "user", [text("bg task")]),
        message(2, "assistant", [text("started")]),
        message(3, "assistant", [text("done")]),
      ],
      autonomousIds: new Set([3]),
    });

    expect(rows.map((r) => [r.messageId, r.autonomous])).toEqual([
      [1, false],
      [2, false],
      [3, true],
    ]);
  });

  it("空 blocks(或全部被 skip)的消息发射一个 placeholder 行", () => {
    const { rows } = buildTranscriptRows({
      displayMessages: [
        message(2, "assistant", []),
        // 全部被 skip:AskUserQuestion 的 tool_use + result。
        message(3, "assistant", [
          toolUse("toolu-ask", "AskUserQuestion"),
          toolResult("toolu-ask"),
        ]),
      ],
      autonomousIds: new Set(),
    });

    expect(
      rows.map((r) => [
        r.messageId,
        r.item.type,
        r.isFirstOfMessage,
        r.isLastOfMessage,
      ]),
    ).toEqual([
      [2, "placeholder", true, true],
      [3, "placeholder", true, true],
    ]);
  });

  it("live 数据只注入 liveByMessageId 里有 key 的那条消息", () => {
    const { rows } = buildTranscriptRows({
      displayMessages: [
        message(1, "assistant", [text("old")]),
        message(2, "assistant", []),
      ],
      autonomousIds: new Set(),
      liveByMessageId: new Map([[2, { liveTail: "growing" }]]),
    });

    expect(rows.map((r) => [r.messageId, r.item.type])).toEqual([
      [1, "text"],
      [2, "text"],
    ]);
    expect(rows[0].item).toMatchObject({ text: "old" });
    expect(rows[1].item).toMatchObject({ text: "growing", streaming: true });
  });

  it("key 稳定性快照:同一内容的流式形态与落库形态产出逐项相等的行 key 序列", () => {
    // 流式形态:persisted 空,冻结块在 liveBlocks,尾巴在 liveTail。
    const liveForm = buildTranscriptRows({
      displayMessages: [message(2, "assistant", [])],
      autonomousIds: new Set(),
      liveByMessageId: new Map([
        [
          2,
          {
            liveBlocks: [
              text("frozen intro"),
              toolUse("toolu-1"),
              toolResult("toolu-1"),
            ],
            liveTail: "tail text",
          },
        ],
      ]),
    });
    // 落库形态:同样内容全部进 persisted blocks。
    const persistedForm = buildTranscriptRows({
      displayMessages: [
        message(2, "assistant", [
          text("frozen intro"),
          toolUse("toolu-1"),
          toolResult("toolu-1"),
          text("tail text"),
        ]),
      ],
      autonomousIds: new Set(),
    });

    expect(liveForm.rows.map((r) => r.key)).toEqual(
      persistedForm.rows.map((r) => r.key),
    );
  });

  it("流式推进 append-only:liveBlocks 追加正文后旧行 key 不变,只在尾部增行", () => {
    const base = {
      displayMessages: [message(2, "assistant", [])],
      autonomousIds: new Set<number>(),
    };
    const before = buildTranscriptRows({
      ...base,
      liveByMessageId: new Map([
        [
          2,
          {
            liveBlocks: [text("intro"), toolUse("toolu-1"), toolUse("toolu-2")],
          },
        ],
      ]),
    });
    const after = buildTranscriptRows({
      ...base,
      liveByMessageId: new Map([
        [
          2,
          {
            liveBlocks: [
              text("intro"),
              toolUse("toolu-1"),
              toolUse("toolu-2"),
              toolResult("toolu-2"),
              text("conclusion"),
            ],
          },
        ],
      ]),
    });

    const beforeKeys = before.rows.map((r) => r.key);
    const afterKeys = after.rows.map((r) => r.key);
    expect(afterKeys.slice(0, beforeKeys.length)).toEqual(beforeKeys);
    expect(afterKeys.length).toBe(beforeKeys.length + 1);
  });

  it("WeakMap 缓存:非 live 消息同对象重建返回同一行数组引用,live 消息绕过缓存", () => {
    const cache = new WeakMap<TranscriptMessage, TranscriptRow[]>();
    const persisted = message(1, "assistant", [text("stable")]);
    const live = message(2, "assistant", []);
    const args = {
      displayMessages: [persisted, live],
      autonomousIds: new Set<number>(),
      liveByMessageId: new Map([[2, { liveTail: "grow" }]]),
      cache,
    };

    const first = buildTranscriptRows(args);
    const second = buildTranscriptRows(args);

    // persisted 消息:两次构建产出同一 row 对象(=== 引用)→ 行组件 React.memo 恒命中。
    expect(second.rows[0]).toBe(first.rows[0]);
    // live 消息:每次现场重建,不进缓存。
    expect(second.rows[1]).not.toBe(first.rows[1]);
  });
});

// estimateRowSize:虚拟化 estimateSize 的估值表。这组测试是**分析性**校准的执行
// 断言,不是布局测试 —— jsdom 没有真实排版引擎,测不出这些数字是否等于组件实际
// 渲染高度(那部分只能靠 `make dev` 里的人工滚动观察验证)。这里只钉死两件事:
// ①估值确实按 Task 10 校准比例(25.5/22.75,对话流正文 14×1.625→15×1.7)从旧值
// 缩放而来,不是随手改的数;②每一档都严格大于重构前的旧值,防止"只改了兜底、
// 漏了某个 case"的回归 —— 那正是本任务存在的理由(虚拟化行高系统性偏小)。
describe("estimateRowSize", () => {
  const ROW_SIZE_SCALE = 25.5 / 22.75;

  function row(item: TranscriptRowItem): TranscriptRow {
    return {
      autonomous: false,
      isFirstOfMessage: true,
      isLastOfMessage: true,
      item,
      key: "k",
      messageId: 1,
    };
  }

  // [item.type, 重构前(旧字号 14/1.625)的估值] —— 旧值来自 git 历史
  // (transcript-rows.ts 改动前),用来断言"新值 = 旧值 × 比例"而非凭空数字。
  const PRE_CALIBRATION_BASELINE: [TranscriptRowItem["type"], number][] = [
    ["text", 132],
    ["placeholder", 132],
    ["image", 160],
    ["thinking", 40],
    ["compact_boundary", 48],
    ["local_command", 120],
    ["tool", 48],
  ];

  function makeItem(type: TranscriptRowItem["type"]): TranscriptRowItem {
    switch (type) {
      case "text":
        return { text: "hello", type: "text", uiStateKey: "k" };
      case "placeholder":
        return { type: "placeholder" };
      case "image":
        return { block: {} as TranscriptBlock, type: "image", uiStateKey: "k" };
      case "thinking":
        return {
          block: {} as TranscriptBlock,
          streaming: false,
          type: "thinking",
          uiStateKey: "k",
        };
      case "compact_boundary":
        return {
          block: {} as TranscriptBlock,
          type: "compact_boundary",
          uiStateKey: "k",
        };
      case "local_command": {
        const entry: LocalCommandEntry = {
          command: "!ls",
          createdAt: 0,
          id: "cmd-1",
          output: "",
          sessionId: 1,
          status: "done",
        };
        return { entry, type: "local_command" };
      }
      default:
        // tool / plan / tool_permission_request / tool_approval / unknown 全部
        // 落进 estimateRowSize 的 default 分支,取 "tool" 代表整档。
        return { type: "tool", uiStateKey: "k" };
    }
  }

  it("每一档都等于旧值(重构前)按 25.5/22.75 的比例缩放并四舍五入", () => {
    for (const [type, before] of PRE_CALIBRATION_BASELINE) {
      const expected = Math.round(before * ROW_SIZE_SCALE);
      expect(estimateRowSize(row(makeItem(type)))).toBe(expected);
    }
  });

  it("新估值严格大于重构前的旧估值(防止漏改某个 case 导致的系统性偏小)", () => {
    for (const [type, before] of PRE_CALIBRATION_BASELINE) {
      expect(estimateRowSize(row(makeItem(type)))).toBeGreaterThan(before);
    }
  });

  it("text 与 placeholder 共享同一档估值(148),与兜底 chat.tsx:estimateSize 的 148 一致", () => {
    expect(estimateRowSize(row(makeItem("text")))).toBe(148);
    expect(estimateRowSize(row(makeItem("placeholder")))).toBe(148);
  });
});

// estimateRowSizeWithSpacing / isLastRowOfMessage:复审对 Task 10 提出的 Important
// 缺口 —— 上面的 estimateRowSize 只做了字号/间距的乘法缩放(ROW_SIZE_SCALE),没处理
// chat.tsx rowWrapperPad 的加法部分:消息末行 padding pb-5→pb-7(20→28px),消息内
// 分片行 padding pb-2→pb-2.5(8→10px)。这两档 padding 打在与 measureElement 同一个
// div 上(chat.tsx 注释「padding 打在行 wrapper 上,跟随 measureElement 一起计入行
// 高」),所以上面 estimateRowSize 表里的旧值本就隐含了旧 padding;纯乘法只把它放大到
// 20×SCALE≈22.4px / 8×SCALE≈8.97px,分别比新值少 ≈5.6px / ≈1px。
// estimateRowSizeWithSpacing 在 estimateRowSize 之上,按 isLastRowOfMessage(与
// chat.tsx:rowWrapperPad 共用同一份边界判断)补回这段差值。
describe("estimateRowSizeWithSpacing / isLastRowOfMessage", () => {
  const SCALE = 25.5 / 22.75;

  function makeRow(
    messageId: number,
    key: string,
    item: TranscriptRowItem = { text: "x", type: "text", uiStateKey: key },
  ): TranscriptRow {
    return {
      autonomous: false,
      isFirstOfMessage: false,
      isLastOfMessage: false,
      item,
      key,
      messageId,
    };
  }

  it("下一行不存在 → 视为消息末行", () => {
    const rows = [makeRow(1, "a")];
    expect(isLastRowOfMessage(rows, 0)).toBe(true);
  });

  it("下一行属于另一条消息 → 视为消息末行", () => {
    const rows = [makeRow(1, "a"), makeRow(2, "b")];
    expect(isLastRowOfMessage(rows, 0)).toBe(true);
  });

  it("下一行属于同一条消息 → 视为块内行,不是消息末行", () => {
    const rows = [makeRow(1, "a"), makeRow(1, "b")];
    expect(isLastRowOfMessage(rows, 0)).toBe(false);
  });

  it("间距增量常量 = 新 padding - 旧 padding×ROW_SIZE_SCALE:消息末行 ≈5.6px,块内行 ≈1px,末行显著大于块内行", () => {
    expect(ROW_END_PADDING_DELTA).toBeCloseTo(28 - 20 * SCALE, 5);
    expect(ROW_MID_PADDING_DELTA).toBeCloseTo(10 - 8 * SCALE, 5);
    expect(ROW_END_PADDING_DELTA).toBeGreaterThan(ROW_MID_PADDING_DELTA);
  });

  it("同类型行:消息末行估值 = estimateRowSize + ROW_END_PADDING_DELTA(四舍五入),块内行 = + ROW_MID_PADDING_DELTA", () => {
    const rows = [
      makeRow(1, "a", { text: "x", type: "text", uiStateKey: "a" }),
      makeRow(1, "b", { text: "y", type: "text", uiStateKey: "b" }),
    ];
    const base = estimateRowSize(rows[0]);

    expect(estimateRowSizeWithSpacing(rows, 0)).toBe(
      Math.round(base + ROW_MID_PADDING_DELTA),
    );
    expect(estimateRowSizeWithSpacing(rows, 1)).toBe(
      Math.round(base + ROW_END_PADDING_DELTA),
    );
  });

  it("间距增量只取决于是否消息末行,与 item 类型无关(类型差异已经在 estimateRowSize 里算过)", () => {
    const rows = [
      makeRow(1, "a", { type: "tool", uiStateKey: "a" }),
      makeRow(1, "b", {
        block: {} as TranscriptBlock,
        streaming: false,
        type: "thinking",
        uiStateKey: "b",
      }),
    ];
    expect(estimateRowSizeWithSpacing(rows, 0)).toBe(
      Math.round(estimateRowSize(rows[0]) + ROW_MID_PADDING_DELTA),
    );
  });

  it("越界下标返回安全兜底值(不抛异常)", () => {
    expect(estimateRowSizeWithSpacing([], 0)).toBeGreaterThan(0);
  });
});

// ── M3(A) settled/live 拆分 ───────────────────────────────────────────────────
// buildSettledTranscriptRows + applyLiveTranscriptRows 把「messages 稳定、只有 live
// 内容在变」的流式 chunk 路径拆成两部分:settled(rows + 两张索引图,只依赖 messages,
// 可在 ChatTranscript 里 memoize)+ live overlay(每 chunk 只重建 live 消息的行组,
// 复用 settled 的稳定索引)。等价性不变量:applyLive(buildSettled(args), args) 必须与
// 原来的 buildTranscriptRows(args) 逐字节一致 —— 这一组测试把它钉死。
describe("buildSettledTranscriptRows + applyLiveTranscriptRows (M3 split)", () => {
  const base = {
    displayMessages: [
      message(1, "user", [text("u1")]),
      message(2, "assistant", [
        text("a2"),
        toolUse("toolu-2"),
        toolResult("toolu-2"),
      ]),
      message(3, "user", [text("u3")]),
      message(4, "assistant", []),
    ],
    autonomousIds: new Set<number>(),
  };

  // 单条 live 在尾部(流式常态):内容与完整重建逐项相等。
  const liveTail = new Map<number, { liveTail: string }>([
    [4, { liveTail: "growing" }],
  ]);

  function settledResult() {
    return buildSettledTranscriptRows(base);
  }

  it("等价性:no-live 时 applyLive 原样返回 settled(同一引用,零拷贝)", () => {
    const settled = settledResult();
    const full = buildTranscriptRows(base);
    const overlaid = applyLiveTranscriptRows(settled, {
      ...base,
      liveByMessageId: undefined,
    });

    // 引用级 no-op:无 live 时不新建数组 / 不重算索引图。
    expect(overlaid.rows).toBe(settled.rows);
    expect(overlaid.firstRowIndexByMessageId).toBe(
      settled.firstRowIndexByMessageId,
    );
    expect(overlaid.rowIndexByKey).toBe(settled.rowIndexByKey);
    // 且与完整重建语义一致。
    expect(overlaid.rows.map((r) => r.key)).toEqual(
      full.rows.map((r) => r.key),
    );
    expect(overlaid.rows.map((r) => r.messageId)).toEqual(
      full.rows.map((r) => r.messageId),
    );
  });

  it("等价性:单条 live 在尾部时与完整重建逐项相等", () => {
    const settled = settledResult();
    const full = buildTranscriptRows({ ...base, liveByMessageId: liveTail });
    const overlaid = applyLiveTranscriptRows(settled, {
      ...base,
      liveByMessageId: liveTail,
    });

    expect(overlaid.rows.map((r) => r.key)).toEqual(
      full.rows.map((r) => r.key),
    );
    expect(
      overlaid.rows.map((r) => [
        r.messageId,
        r.item.type,
        r.isFirstOfMessage,
        r.isLastOfMessage,
      ]),
    ).toEqual(
      full.rows.map((r) => [
        r.messageId,
        r.item.type,
        r.isFirstOfMessage,
        r.isLastOfMessage,
      ]),
    );
    // live 行内容一致。
    expect(overlaid.rows.at(-1)?.item).toMatchObject({
      text: "growing",
      streaming: true,
    });
  });

  it("性能性质:非 live 行与 settled 共享同一 row 引用(live overlay 不重建它们)", () => {
    const settled = settledResult();
    const overlaid = applyLiveTranscriptRows(settled, {
      ...base,
      liveByMessageId: liveTail,
    });

    // 前 3 条消息不是 live → 逐行与 settled 引用相同(memo 恒命中)。
    for (let i = 0; i < settled.rows.length - 1; i++) {
      expect(overlaid.rows[i]).toBe(settled.rows[i]);
    }
    // 最后一条(live)被替换成新行。
    expect(overlaid.rows.at(-1)).not.toBe(settled.rows.at(-1));
  });

  it("索引图稳定:live 在尾部时 firstRowIndexByMessageId 复用 settled 的同一引用", () => {
    const settled = settledResult();
    const overlaid = applyLiveTranscriptRows(settled, {
      ...base,
      liveByMessageId: liveTail,
    });
    expect(overlaid.firstRowIndexByMessageId).toBe(
      settled.firstRowIndexByMessageId,
    );
    // rowIndexByKey 是新 map(live 行 key 被替换),但 settled 里非 live 行的条目不变。
    for (const [k, v] of settled.rowIndexByKey) {
      if (k.startsWith("message:4:")) continue;
      expect(overlaid.rowIndexByKey.get(k)).toBe(v);
    }
  });

  it("等价性:多条 live 都在尾部时与完整重建一致", () => {
    const multi = {
      ...base,
      displayMessages: [
        message(1, "user", [text("u1")]),
        message(2, "assistant", []),
        message(3, "assistant", []),
      ],
    };
    const live = new Map([
      [2, { liveTail: "a" }],
      [
        3,
        { liveBlocks: [text("b"), toolUse("toolu-3"), toolResult("toolu-3")] },
      ],
    ]);
    const full = buildTranscriptRows({ ...multi, liveByMessageId: live });
    const settled = buildSettledTranscriptRows(multi);
    const overlaid = applyLiveTranscriptRows(settled, {
      ...multi,
      liveByMessageId: live,
    });

    expect(overlaid.rows.map((r) => r.key)).toEqual(
      full.rows.map((r) => r.key),
    );
    expect(overlaid.rows.map((r) => r.messageId)).toEqual(
      full.rows.map((r) => r.messageId),
    );
  });

  it("等价性:live 不在尾部(历史消息仍在流)时回退到完整重建,仍逐项一致", () => {
    // 消息 2(live)后面还有消息 3、4 —— 不满足「live 是尾部连续后缀」。
    const liveMid = new Map([[2, { liveTail: "mid-stream" }]]);
    const full = buildTranscriptRows({ ...base, liveByMessageId: liveMid });
    const settled = settledResult();
    const overlaid = applyLiveTranscriptRows(settled, {
      ...base,
      liveByMessageId: liveMid,
    });

    expect(overlaid.rows.map((r) => r.key)).toEqual(
      full.rows.map((r) => r.key),
    );
    expect(overlaid.rows.map((r) => r.messageId)).toEqual(
      full.rows.map((r) => r.messageId),
    );
    // 回退路径产出的索引图是新鲜的(不能复用 settled 的,因为 live 组后还有消息)。
    expect(overlaid.firstRowIndexByMessageId).not.toBe(
      settled.firstRowIndexByMessageId,
    );
  });

  it("等价性:live 尾部后面有本地命令时回退到完整重建,仍逐项一致", () => {
    const cmds = [
      {
        command: "!ls",
        createdAt: 50,
        id: "cmd-1",
        output: "",
        sessionId: 1,
        status: "done",
      } as LocalCommandEntry,
    ];
    const args = { ...base, localCommands: cmds };
    const full = buildTranscriptRows({ ...args, liveByMessageId: liveTail });
    const settled = buildSettledTranscriptRows(args);
    const overlaid = applyLiveTranscriptRows(settled, {
      ...args,
      liveByMessageId: liveTail,
    });

    expect(overlaid.rows.map((r) => r.key)).toEqual(
      full.rows.map((r) => r.key),
    );
  });

  it("groupByMessageId:每条消息的行组 [start,length) 与首行下标/行数一致", () => {
    const settled = settledResult();
    for (const m of base.displayMessages) {
      const g = settled.groupByMessageId.get(m.id);
      expect(g).toBeTruthy();
      expect(g!.start).toBe(settled.firstRowIndexByMessageId.get(m.id));
      // 组跨 [start, start+length) 且全部属于该消息。
      for (let i = g!.start; i < g!.start + g!.length; i++) {
        expect(settled.rows[i].messageId).toBe(m.id);
      }
      if (g!.start + g!.length < settled.rows.length) {
        expect(settled.rows[g!.start + g!.length].messageId).not.toBe(m.id);
      }
    }
  });

  it("流式→落库 key 稳定性在拆分路径下仍然成立", () => {
    // 流式形态:persisted 空,冻结块在 liveBlocks,尾巴在 liveTail。
    const liveForm = {
      displayMessages: [message(2, "assistant", [])],
      autonomousIds: new Set<number>(),
      liveByMessageId: new Map([
        [
          2,
          {
            liveBlocks: [
              text("frozen"),
              toolUse("toolu-1"),
              toolResult("toolu-1"),
            ],
            liveTail: "tail",
          },
        ],
      ]),
    };
    const { liveByMessageId, ...settledArgs } = liveForm;
    const overlaid = applyLiveTranscriptRows(
      buildSettledTranscriptRows(settledArgs),
      { ...settledArgs, liveByMessageId },
    );
    // 落库形态:同样内容全部进 persisted blocks。
    const persisted = buildTranscriptRows({
      displayMessages: [
        message(2, "assistant", [
          text("frozen"),
          toolUse("toolu-1"),
          toolResult("toolu-1"),
          text("tail"),
        ]),
      ],
      autonomousIds: new Set(),
    });

    expect(overlaid.rows.map((r) => r.key)).toEqual(
      persisted.rows.map((r) => r.key),
    );
  });

  it("autonomous / sourceDevice 在 overlay 重建 live 行时仍正确传递", () => {
    const args = {
      displayMessages: [
        message(1, "user", [text("hi")]),
        message(2, "assistant", []),
      ],
      autonomousIds: new Set([2]),
      sourceByMessageId: new Map([[1, "iPhone"]]),
    };
    const live = new Map([[2, { liveTail: "x" }]]);
    const overlaid = applyLiveTranscriptRows(buildSettledTranscriptRows(args), {
      ...args,
      liveByMessageId: live,
    });
    expect(overlaid.rows.find((r) => r.messageId === 1)?.sourceDevice).toBe(
      "iPhone",
    );
    expect(overlaid.rows.find((r) => r.messageId === 2)?.autonomous).toBe(true);
  });
});

// ─── 活动块聚合(对话流密度)──────────────────────────────────────────────────
// 数据层只回答两件事:哪些连续步骤折进同一个活动块,以及折叠态组头必须说什么。
// 渲染 / 样式 / i18n 全在上层 —— 这里的断言都是纯数据形状。
//
// 判据来源不在本文件:每一步落哪一档 / 哪个类目由 canonical-tool/tier.ts 算
// (canonical.kind → input shape → 中性兜底,全程不查工具名表)。

function thinkingBlock(t: string): TranscriptBlock {
  return { type: "thinking", text: t } as TranscriptBlock;
}

function readUse(id: string, path = "a.ts"): TranscriptBlock {
  return toolUse(id, "Read", { toolInput: { path } });
}

function editUse(
  id: string,
  files: { path: string; plus: number; minus: number }[],
): TranscriptBlock {
  return toolUse(id, "Edit", {
    canonical: {
      kind: "file.edit",
      fileEdit: {
        files: files.map((f) => ({ ...f, hunks: [], kind: "modified" })),
      },
    },
    toolInput: { file_path: files[0]?.path },
  } as unknown as Partial<TranscriptBlock>);
}

function writeUse(id: string, path = "new.ts"): TranscriptBlock {
  return toolUse(id, "Write", {
    canonical: {
      kind: "file.write",
      fileWrite: { bytes: 1, content: "x", lines: 1, path },
    },
    toolInput: { content: "x", file_path: path },
  } as unknown as Partial<TranscriptBlock>);
}

function otherUse(id: string): TranscriptBlock {
  return toolUse(id, "mcp__acme__execute", { toolInput: { foo: "bar" } });
}

function spawnUse(id: string): TranscriptBlock {
  return toolUse(id, "Agent", {
    canonical: { kind: "agent.spawn", agentSpawn: { taskId: id } },
  } as unknown as Partial<TranscriptBlock>);
}

function failedResult(id: string): TranscriptBlock {
  return toolResult(id, "boom", { isError: true });
}

function activityAt(
  items: VisibleRenderItem[],
  idx: number,
): VisibleActivityItem {
  const item = items[idx];
  if (item.type !== "activity") {
    throw new Error(`items[${idx}] expected activity, got ${item.type}`);
  }
  return item;
}

function stepLabels(activity: VisibleActivityItem): string[] {
  return activity.steps.map((step) =>
    step.type === "thinking"
      ? `thinking:${step.block.text}`
      : `tool:${step.toolBlock?.toolName}`,
  );
}

describe("活动块聚合", () => {
  it("连续的思考与工具折成一个活动块,步骤保持发生顺序", () => {
    const items = buildRenderItems({
      messageId: 5,
      blocks: [
        thinkingBlock("t1"),
        readUse("toolu-1"),
        toolResult("toolu-1", "ok"),
        toolUse("toolu-2"),
        toolResult("toolu-2", "ok"),
      ],
    });

    expect(items.map((i) => i.type)).toEqual(["activity"]);
    const activity = activityAt(items, 0);
    expect(stepLabels(activity)).toEqual([
      "thinking:t1",
      "tool:Read",
      "tool:Bash",
    ]);
    // 步骤仍带着自己的 tool_use / tool_result 配对结果 —— 展开后能看到参数与结果。
    expect(activity.steps[1]).toMatchObject({
      type: "tool",
      toolBlock: { toolUseId: "toolu-1" },
      resultBlock: { text: "ok" },
    });
    expect(activity.summary.steps).toBe(3);
  });

  it("组头汇总按固定顺序输出,超过 4 类时截断并置 truncated", () => {
    const items = buildRenderItems({
      messageId: 1,
      // 故意打乱发生顺序:组头顺序是固定的,不跟随出现次序。
      blocks: [
        toolUse("toolu-cmd1"),
        otherUse("toolu-other"),
        failedResult("toolu-other"),
        thinkingBlock("a"),
        thinkingBlock("b"),
        readUse("toolu-read"),
        editUse("toolu-edit", [
          { minus: 1, path: "a.ts", plus: 3 },
          { minus: 2, path: "b.ts", plus: 2 },
        ]),
        writeUse("toolu-write"),
        toolUse("toolu-cmd2"),
      ],
    });

    const { summary } = activityAt(items, 0);
    expect(summary.parts).toEqual([
      { category: "thinking", count: 2 },
      { category: "read", count: 1 },
      { category: "edit", count: 1, files: 2, minus: 3, plus: 5 },
      { category: "write", count: 1 },
    ]);
    expect(summary.truncated).toBe(true);
    // 8 步:失败的那条 tool_result 配对进它的 tool_use,不另算一步。
    expect(summary.steps).toBe(8);
    // 失败计数不参与截断 —— 组头是折叠态唯一的信息出口。
    expect(summary.failures).toBe(1);
  });

  it("失败与类目正交:失败的 Read 仍计一次查阅,且四类以内不截断", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        readUse("toolu-1"),
        failedResult("toolu-1"),
        readUse("toolu-2"),
        toolResult("toolu-2", "ok"),
        toolUse("toolu-3"),
        failedResult("toolu-3"),
      ],
    });

    const { summary } = activityAt(items, 0);
    expect(summary.parts).toEqual([
      { category: "read", count: 2 },
      { category: "command", count: 1 },
    ]);
    expect(summary.truncated).toBe(false);
    expect(summary.failures).toBe(2);
  });

  // 组头失败计数与活动行的红色标记必须是同一判据。活动行把「本轮已经失败终结、
  // 这一步却始终没配到 tool_result」也算失败(facts.ts 的 stepFacts),组头漏算就
  // 会出现「展开是三行红的、折叠说一个失败都没有」—— 折叠是收起,不是让发生过
  // 的事消失。
  it("本轮已失败终结时,没配到结果的一步也计进组头失败数", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        readUse("toolu-1"),
        toolResult("toolu-1", "ok"),
        toolUse("toolu-2"),
        toolUse("toolu-3"),
      ],
    });
    const { steps } = activityAt(items, 0);

    // 转录里的一轮永远按「运行中」处理 —— 没有结果不等于失败,计数不变。
    expect(summarizeActivity(steps).failures).toBe(0);
    // 调用方(子代理那层)声明这一轮以失败终结时,那两步归属失败。
    expect(summarizeActivity(steps, true).failures).toBe(2);
  });

  // 命令类结果把失败写在结果 JSON 里(exitCode / status),isError 只在 item 自身
  // 失败时才置位 —— 一条 exit 1 的命令 isError 是 false。组头只看 isError 就会
  // 宣称「零失败」,而同一条结果在 RawToolCard 里一直是按错误渲染的。
  it("命令以非零退出码结束但结果没标 isError 时,仍计进组头失败数", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        toolUse("toolu-1"),
        toolResult(
          "toolu-1",
          '{"exitCode":1,"output":"boom","status":"completed"}',
        ),
        toolUse("toolu-2"),
        toolResult(
          "toolu-2",
          '{"exitCode":0,"output":"ok","status":"completed"}',
        ),
        toolUse("toolu-3"),
        toolResult("toolu-3", '{"output":"gone","status":"interrupted"}'),
      ],
    });

    expect(activityAt(items, 0).summary.failures).toBe(2);
  });

  it("同一文件改两次算一个文件,增删行累加", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        editUse("toolu-1", [{ minus: 1, path: "same.ts", plus: 2 }]),
        editUse("toolu-2", [{ minus: 3, path: "same.ts", plus: 4 }]),
      ],
    });

    expect(activityAt(items, 0).summary.parts).toEqual([
      { category: "edit", count: 2, files: 1, minus: 4, plus: 6 },
    ]);
  });

  it("正文打断聚合,前后各自成块且不重排", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        readUse("toolu-1"),
        readUse("toolu-2"),
        text("中间的结论"),
        toolUse("toolu-3"),
        toolUse("toolu-4"),
      ],
    });

    expect(items.map((i) => i.type)).toEqual(["activity", "text", "activity"]);
    expect(stepLabels(activityAt(items, 0))).toEqual([
      "tool:Read",
      "tool:Read",
    ]);
    expect(stepLabels(activityAt(items, 2))).toEqual([
      "tool:Bash",
      "tool:Bash",
    ]);
  });

  it("出组项(子代理 / 未决审批 / 提问)打断聚合并保持独立可见", () => {
    const pendingPerm = {
      type: "tool_permission_request",
      toolPermission: { requestId: "req-1", toolName: "Bash" },
    } as unknown as TranscriptBlock;
    const askBlock = { type: "ask_user_question" } as TranscriptBlock;

    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        readUse("toolu-1"),
        readUse("toolu-2"),
        spawnUse("toolu-spawn"),
        readUse("toolu-3"),
        readUse("toolu-4"),
        pendingPerm,
        readUse("toolu-5"),
        readUse("toolu-6"),
        askBlock,
        readUse("toolu-7"),
        readUse("toolu-8"),
      ],
    });

    expect(items.map((i) => i.type)).toEqual([
      "activity",
      "tool", // 子代理卡
      "activity",
      "tool_permission_request",
      "activity",
      "tool", // 提问卡
      "activity",
    ]);
    expect(items[1]).toMatchObject({
      type: "tool",
      toolBlock: { toolUseId: "toolu-spawn" },
    });
    // canonical 缺失的 ask_user_question 也不许进组(阻塞用户的卡片永不进组)。
    expect(items[5]).toMatchObject({
      type: "tool",
      toolBlock: { type: "ask_user_question" },
    });
  });

  it("内置写工具审批与计划卡同样打断聚合(阻塞用户的卡片永不进组)", () => {
    const approvalBlock = {
      type: "tool_approval",
      toolApproval: {
        requestId: "org-1",
        status: "pending",
        toolKey: "org",
        toolName: "org_create_department",
      },
    } as unknown as TranscriptBlock;
    const planBlock = {
      type: "plan",
      canonical: {
        kind: "plan.update",
        planUpdate: { actions: [{ kind: "approve" }], steps: [], text: "" },
      },
    } as unknown as TranscriptBlock;

    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        readUse("toolu-1"),
        readUse("toolu-2"),
        approvalBlock,
        readUse("toolu-3"),
        readUse("toolu-4"),
        planBlock,
        readUse("toolu-5"),
        readUse("toolu-6"),
      ],
    });

    expect(items.map((i) => i.type)).toEqual([
      "activity",
      "tool_approval",
      "activity",
      "plan",
      "activity",
    ]);
  });

  it("单条不成组:一段活动只有一步时也是活动项(壳由渲染层省掉),不再退回整卡", () => {
    const items = buildRenderItems({
      messageId: 5,
      blocks: [
        text("前言"),
        readUse("toolu-1"),
        toolResult("toolu-1", "ok"),
        text("中间"),
        thinkingBlock("独立思考"),
        text("结论"),
      ],
    });

    // 一条 assistant 消息只由「正文 / 活动块 / 出组卡片 / 脚注」四种东西组成 ——
    // 落单的一次工具调用与一段已完成的思考都不再是各自一张整卡。
    expect(items.map((i) => i.type)).toEqual([
      "text",
      "activity",
      "text",
      "activity",
      "text",
    ]);
    const lone = activityAt(items, 1);
    expect(stepLabels(lone)).toEqual(["tool:Read"]);
    // 组内那一步的 key 与「它单独成行」时字节一致 —— 已持久化的展开态零迁移。
    expect(lone.steps[0].uiStateKey).toBe("message:5:tool:tool:toolu-1");
    // 活动项自身的 key 与多步块同构:一段从 1 步长到 N 步时行 key 不漂移。
    expect(lone.uiStateKey).toBe("message:5:activity:tool:toolu-1");
    expect(stepLabels(activityAt(items, 3))).toEqual(["thinking:独立思考"]);
  });

  it("claudecode 的 Read(file_path 形状)计入「查阅」而不是「其它」", () => {
    const items = buildRenderItems({
      messageId: 1,
      blocks: [
        toolUse("toolu-1", "Read", { toolInput: { file_path: "/repo/a.ts" } }),
        toolUse("toolu-2", "Read", { toolInput: { file_path: "/repo/b.ts" } }),
      ],
    });

    expect(activityAt(items, 0).summary.parts).toEqual([
      { category: "read", count: 2 },
    ]);
  });

  it("流式思考不进组(它仍是承载 live tail 的整卡)", () => {
    const items = buildRenderItems({
      messageId: 1,
      liveBlocks: [readUse("toolu-1"), readUse("toolu-2")],
      liveThinking: "still thinking…",
    });

    expect(items.map((i) => i.type)).toEqual(["activity", "thinking"]);
    expect(items[1]).toMatchObject({ type: "thinking", streaming: true });
  });

  it("活动块 uiStateKey 取首步身份,组内步骤保留各自原有的 key", () => {
    const items = buildRenderItems({
      messageId: 5,
      blocks: [readUse("toolu-1"), readUse("toolu-2")],
    });

    const activity = activityAt(items, 0);
    expect(activity.uiStateKey).toBe("message:5:activity:tool:toolu-1");
    // 步骤 key 与「这一步单独成行」时字节一致 —— 已持久化的展开态零迁移。
    expect(activity.steps.map((s) => s.uiStateKey)).toEqual([
      "message:5:tool:tool:toolu-1",
      "message:5:tool:tool:toolu-2",
    ]);
  });

  it("首步无身份(思考)时活动块 uiStateKey 回退到可见下标", () => {
    const items = buildRenderItems({
      messageId: 5,
      blocks: [text("前言"), thinkingBlock("t"), readUse("toolu-1")],
    });

    expect(activityAt(items, 1).uiStateKey).toBe("message:5:activity:1");
  });

  it("流式生长:活动块成组后追加步骤,组头 key 不漂移", () => {
    const base = {
      displayMessages: [message(2, "assistant", [])],
      autonomousIds: new Set<number>(),
    };
    const before = buildTranscriptRows({
      ...base,
      liveByMessageId: new Map([
        [2, { liveBlocks: [text("intro"), readUse("toolu-1"), toolUse("t2")] }],
      ]),
    });
    const after = buildTranscriptRows({
      ...base,
      liveByMessageId: new Map([
        [
          2,
          {
            liveBlocks: [
              text("intro"),
              readUse("toolu-1"),
              toolUse("t2"),
              toolResult("t2", "ok"),
              readUse("toolu-3"),
            ],
          },
        ],
      ]),
    });

    // 一个活动块 = 一个虚拟行:追加步骤不增行,组头 key 也不变。
    expect(before.rows.map((r) => r.key)).toEqual([
      "message:2:text:0",
      "message:2:activity:tool:toolu-1",
    ]);
    expect(after.rows.map((r) => r.key)).toEqual(before.rows.map((r) => r.key));
  });

  it("estimateRowSize 覆盖 activity(不落卡片兜底档)", () => {
    const activityRow: TranscriptRow = {
      autonomous: false,
      isFirstOfMessage: true,
      isLastOfMessage: true,
      item: {
        steps: [],
        summary: { failures: 0, parts: [], steps: 0, truncated: false },
        type: "activity",
        uiStateKey: "k",
      },
      key: "k",
      messageId: 1,
    };
    const cardRow: TranscriptRow = {
      ...activityRow,
      item: { type: "tool", uiStateKey: "k" },
    };

    // 折叠态组头是单行,与折叠态 thinking 同档,比整张卡片矮。
    expect(estimateRowSize(activityRow)).toBe(45);
    expect(estimateRowSize(activityRow)).toBeLessThan(estimateRowSize(cardRow));
  });
});
