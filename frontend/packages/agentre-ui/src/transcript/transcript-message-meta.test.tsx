import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

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

function renderRow(
  row: TranscriptRow,
  extra?: Partial<ComponentProps<typeof TranscriptRowView>>,
) {
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
          {...extra}
        />
      </TranscriptRenderContext.Provider>
    </TooltipProvider>,
  );
}

describe("assistant message meta token counts", () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(10_000);
  });
  afterEach(() => {
    vi.useRealTimers();
  });

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

  it("Given reported usage without duration, When the message is rendered, Then token counters remain visible", () => {
    renderRow(
      assistantRow({
        promptTokens: 1200,
        completionTokens: 340,
        durationMs: 0,
      }),
    );
    expect(screen.getByTestId("message-token-counts")).toBeDefined();
  });

  // 只有缓存命中的轮次:prompt/completion 是 0 但确实上报过用量,不能当成「没上报」。
  it("Given only cached tokens, When the message is rendered, Then the counters remain visible", () => {
    renderRow(assistantRow({ cachedTokens: 1200 }));

    expect(screen.getByTestId("message-token-counts")).toBeDefined();
  });

  it("Given a streaming assistant with durationMs 0, When rendered, Then the footer still shows the model and waiting first-token", () => {
    vi.setSystemTime(1800);
    renderRow(assistantRow({ durationMs: 0, model: "" }), {
      showIndicator: true,
      liveTurn: {
        startedAt: 1000,
        firstTokenAt: null,
        generationMs: 0,
        burstStartedAt: null,
        promptTokens: 0,
        completionTokens: 0,
        cachedTokens: 0,
        cacheCreationTokens: 0,
        reasoningTokens: 0,
        model: "claude-sonnet-4",
        liveText: "",
      },
    });

    expect(screen.getByText("claude-sonnet-4")).toBeDefined();
    expect(screen.getByTestId("message-first-token").textContent).toBe("0.8s");
    expect(screen.queryByTestId("message-token-counts")).toBeNull();
  });

  it("Given streaming text before usage, When rendered, Then completion is marked approximate and speed is shown", () => {
    vi.setSystemTime(3400);
    renderRow(assistantRow({ durationMs: 0, model: "claude-sonnet-4" }), {
      liveTail: "a".repeat(80),
      showIndicator: true,
      liveTurn: {
        startedAt: 1000,
        firstTokenAt: 1420,
        generationMs: 0,
        burstStartedAt: 1420,
        promptTokens: 0,
        completionTokens: 0,
        cachedTokens: 0,
        cacheCreationTokens: 0,
        reasoningTokens: 0,
        model: "claude-sonnet-4",
        liveText: "a".repeat(80),
      },
    });

    expect(screen.getByTestId("message-token-counts").textContent).toContain(
      "~",
    );
    expect(screen.getByTestId("message-tokens-per-sec").textContent).toContain(
      "tok/s",
    );
    expect(screen.getByTestId("message-first-token").textContent).toMatch(
      /0\.4s/,
    );
    expect(screen.getByTestId("message-first-token").textContent).not.toMatch(
      /first token|首 token/i,
    );
  });

  it("Given a finished message with first-token and speed, When rendered, Then both appear without cumulative labels", () => {
    renderRow(
      assistantRow({
        durationMs: 8600,
        model: "claude-sonnet-4",
        promptTokens: 22000,
        completionTokens: 1400,
        firstTokenMs: 420,
        tokensPerSec: 48,
      }),
    );

    expect(screen.getByTestId("message-first-token").textContent).toMatch(
      /0\.4s/,
    );
    expect(screen.getByTestId("message-first-token").textContent).not.toMatch(
      /first token|首 token/i,
    );
    expect(screen.getByTestId("message-tokens-per-sec").textContent).toContain(
      "48",
    );
    expect(screen.queryByText(/累计/)).toBeNull();
    expect(screen.queryByText(/最近一次/)).toBeNull();
  });

  it("Given an old finished message without first-token fields, When rendered, Then those fields stay hidden", () => {
    renderRow(
      assistantRow({
        durationMs: 4500,
        model: "claude-sonnet-4",
        promptTokens: 10,
        completionTokens: 4,
      }),
    );

    expect(screen.queryByTestId("message-first-token")).toBeNull();
    expect(screen.queryByTestId("message-tokens-per-sec")).toBeNull();
    expect(screen.getByText("4.5s")).toBeDefined();
  });
});
