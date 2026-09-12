import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";

import type { TranscriptRow } from "./transcript-rows";

// 宿主契约:TranscriptRowView 是共享组件,任何宿主(桌面端、SaaS 前端)都可能
// 直接裸渲染它,不能假设宿主在 React 树上层包了 Radix TooltipProvider。
// agentre-server 的 Session 页就是这么用的 —— radix-ui 1.4 的 Tooltip Root
// 在 render 阶段就会抛 "`Tooltip` must be used within `TooltipProvider`"。
// 这里刻意不包任何 Provider,复刻报错的宿主环境。
function assistantRow(): TranscriptRow {
  return {
    key: "message:9:text:0",
    messageId: 9,
    message: {
      id: 9,
      role: "assistant",
      blocks: [{ type: "text", text: "hi" }],
      createtime: 0,
      durationMs: 4500,
      model: "claude-sonnet-4",
      promptTokens: 1200,
      completionTokens: 340,
    },
    item: {
      type: "text",
      uiStateKey: "message:9:text:0",
      text: "hi",
    },
    isFirstOfMessage: true,
    isLastOfMessage: true,
    autonomous: false,
  } as unknown as TranscriptRow;
}

describe("TranscriptRowView host contract", () => {
  it("Given a host without any TooltipProvider ancestor, When an assistant row with usage renders, Then the meta row mounts without throwing", () => {
    expect(() =>
      render(
        <TranscriptRenderContext.Provider
          value={{
            agentName: "OpenClaw",
            agentAvatar: <span />,
            sessionId: 42,
          }}
        >
          <TranscriptRowView
            row={assistantRow()}
            liveTail=""
            liveBlocks={undefined}
            liveRetry={null}
            showIndicator={false}
            compacting={false}
            reconnecting={false}
          />
        </TranscriptRenderContext.Provider>,
      ),
    ).not.toThrow();

    expect(screen.getByTestId("message-token-counts")).toBeDefined();
  });
});
