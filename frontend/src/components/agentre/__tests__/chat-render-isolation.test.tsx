import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

// 重渲隔离探针:把 ActivityBlock(工具步骤如今的渲染出口)换成渲染计数器,
// 验证流式 chunk 期间 persisted 消息的工具行不重渲 —— 这是本次性能修复的另一半
// (行级 memo + WeakMap 行缓存 + TranscriptRenderContext 稳定值)。若 context
// value 或行对象在 chunk 间失稳,memo 被击穿,这里立刻红。
const probe = vi.hoisted(() => ({
  renders: new Map<string, number>(),
}));

// ActivityBlock 已搬进共享包,宿主只能从包的公开出口拿到它 —— 所以探针也换成
// 「原样透传整个 barrel、只替掉 ActivityBlock」的 per-file mock。
vi.mock("@agentre-ai/agentre-ui", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@agentre-ai/agentre-ui")>()),
  ActivityBlock: ({
    steps,
  }: {
    steps: { toolBlock?: { toolUseId?: string } }[];
  }) => {
    const key = steps[0]?.toolBlock?.toolUseId ?? "?";
    probe.renders.set(key, (probe.renders.get(key) ?? 0) + 1);
    return <div data-testid="probe-tool-card" data-tool={key} />;
  },
}));

import { ChatTranscript } from "@/components/agentre/chat";
import type { ChatBlockData } from "@/stores/chat-streams-store";
import type { chat_svc } from "../../../../wailsjs/go/models";

function message(
  id: number,
  role: "user" | "assistant",
  blocks: ChatBlockData[],
): chat_svc.ChatMessage {
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
  } as chat_svc.ChatMessage;
}

function toolPair(toolUseId: string): ChatBlockData[] {
  return [
    {
      toolInput: { command: `echo ${toolUseId}` },
      toolName: "Bash",
      toolUseId,
      type: "tool_use",
    } as ChatBlockData,
    { text: "done", toolUseId, type: "tool_result" } as ChatBlockData,
  ];
}

describe("ChatTranscript live re-render isolation", () => {
  it("Given persisted tool rows, When live chunks stream in, Then only the live message re-renders", () => {
    // 两次调用之间隔一句正文:正文打断活动块聚合,于是这两次调用各自成行
    // (单条不成组),探针才数得到「持久化行有没有被流式 chunk 带着重渲」。
    const persisted = message(1, "assistant", [
      ...toolPair("toolu-old-1"),
      { text: "then", type: "text" } as ChatBlockData,
      ...toolPair("toolu-old-2"),
    ]);
    const live = message(2, "assistant", []);
    const messages = [persisted, live];

    const transcript = (liveDelta: string) => (
      <ChatTranscript
        liveByMessageId={
          new Map([
            [2, { liveTail: liveDelta, liveBlocks: toolPair("toolu-live-1") }],
          ])
        }
        agentColor="agent-1"
        agentName="A"
        messages={messages}
        onStopLocalCommand={() => undefined}
        streaming
      />
    );

    const { rerender } = render(transcript("chunk"));
    expect(screen.getAllByTestId("probe-tool-card").length).toBe(3);
    const persistedAfterMount = [
      probe.renders.get("toolu-old-1"),
      probe.renders.get("toolu-old-2"),
    ];
    const liveAfterMount = probe.renders.get("toolu-live-1")!;

    rerender(transcript("chunk chunk"));
    rerender(transcript("chunk chunk chunk"));
    rerender(transcript("chunk chunk chunk chunk"));

    // persisted 消息的 tool 行:行对象来自 WeakMap 缓存、live props 恒为空值、
    // context value 稳定 → memo 恒命中,3 个 chunk 渲染计数零增长。
    expect([
      probe.renders.get("toolu-old-1"),
      probe.renders.get("toolu-old-2"),
    ]).toEqual(persistedAfterMount);
    // live 消息的行每 chunk 现场重建 → 一定重渲(探针本身在工作的 sanity check)。
    expect(probe.renders.get("toolu-live-1")!).toBeGreaterThan(liveAfterMount);
  });
});
