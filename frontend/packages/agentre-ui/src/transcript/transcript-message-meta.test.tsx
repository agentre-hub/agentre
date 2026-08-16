import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TooltipProvider } from "../ui/tooltip";

import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";

import type { TranscriptRow } from "./transcript-rows";

function assistantRow(
  message: Partial<Record<string, unknown>>,
): TranscriptRow {
  return {
    key: "message:9:text:0",
    messageId: 9,
    message: {
      id: 9,
      role: "assistant",
      blocks: [{ type: "text", text: "391" }],
      createtime: 0,
      durationMs: 4500,
      model: "",
      promptTokens: 0,
      completionTokens: 0,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
      ...message,
    },
    item: {
      type: "text",
      uiStateKey: "message:9:text:0",
      text: "391",
    },
    isFirstOfMessage: true,
    isLastOfMessage: true,
    autonomous: false,
  } as unknown as TranscriptRow;
}

function renderRow(row: TranscriptRow) {
  render(
    <TooltipProvider>
      <TranscriptRenderContext.Provider
        value={{ agentName: "OpenClaw", agentAvatar: <span />, sessionId: 42 }}
      >
        <TranscriptRowView
          row={row}
          liveTail=""
          liveBlocks={undefined}
          liveRetry={null}
          showIndicator={false}
          compacting={false}
          reconnecting={false}
        />
      </TranscriptRenderContext.Provider>
    </TooltipProvider>,
  );
}

describe("assistant message meta token counts", () => {
  // 真实网关(OpenClaw)一整轮不发 usage,消息上所有 token 列都是 0。旧实现照样渲染
  // 「↑0 ↓0」，把「没上报」显示成「用了 0 个 token」。
  it("Given a backend that reported no usage, When the message is rendered, Then no token counters are shown", () => {
    renderRow(assistantRow({}));

    expect(screen.queryByTestId("message-token-counts")).toBeNull();
    expect(screen.getByText("4.5s")).toBeDefined();
  });

  it("Given reported usage, When the message is rendered, Then the token counters are shown", () => {
    renderRow(
      assistantRow({
        promptTokens: 17399,
        completionTokens: 259,
        model: "huu/gpt-5.6-sol",
      }),
    );

    const counts = screen.getByTestId("message-token-counts");
    expect(counts.textContent).toContain("17,399");
    expect(counts.textContent).toContain("259");
    expect(screen.getByText("huu/gpt-5.6-sol")).toBeDefined();
  });

  // 只有缓存命中的轮次:prompt/completion 是 0 但确实上报过用量,不能当成「没上报」。
  it("Given only cached tokens, When the message is rendered, Then the counters remain visible", () => {
    renderRow(assistantRow({ cachedTokens: 1200 }));

    expect(screen.getByTestId("message-token-counts")).toBeDefined();
  });
});
