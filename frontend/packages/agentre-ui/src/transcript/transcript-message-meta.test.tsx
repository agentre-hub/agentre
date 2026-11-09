import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ComponentProps } from "react";

import { TooltipProvider } from "../ui/tooltip";

// 复制走剪贴板副作用；这里只关心「点复制不开明细」。
vi.mock("../lib/clipboard-toast", async (importOriginal) => ({
  ...(await importOriginal<typeof import("../lib/clipboard-toast")>()),
  copyTextWithToast: () => Promise.resolve(true),
}));

import {
  TranscriptRenderContext,
  TranscriptRowView,
  type TranscriptRenderContextValue,
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
  ctx?: Partial<TranscriptRenderContextValue>,
) {
  return render(
    <TooltipProvider>
      <TranscriptRenderContext.Provider
        value={{
          agentName: "OpenClaw",
          agentAvatar: <span />,
          sessionId: 42,
          ...ctx,
        }}
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

// ─── 紧凑档（手机宽度的统计行） ───────────────────────────────────────────────

// happy-dom 不排版：可用宽度与各段宽度由这里给。用时一段按字数量（运行中它会变长），
// 其余各段取固定值，便于把让位的每一档卡准。
const RULER_WIDTHS: Record<string, number> = {
  model: 70,
  "model-min": 50,
  counts: 110,
  speed: 60,
  more: 12,
};
let availableWidth = 0;
let resizeCallbacks: Array<() => void> = [];

function stubLayout() {
  resizeCallbacks = [];
  vi.stubGlobal(
    "ResizeObserver",
    class {
      private readonly cb: () => void;
      constructor(cb: () => void) {
        this.cb = cb;
        resizeCallbacks.push(cb);
      }
      observe() {}
      unobserve() {}
      disconnect() {
        resizeCallbacks = resizeCallbacks.filter((c) => c !== this.cb);
      }
    },
  );
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      const kind = this.getAttribute("data-meta-ruler");
      let width = 0;
      if (this.getAttribute("data-testid") === "message-meta-line") {
        width = availableWidth;
      } else if (kind === "duration") {
        width = (this.textContent ?? "").length * 10;
      } else if (kind) {
        width = RULER_WIDTHS[kind] ?? 0;
      }
      return {
        width,
        height: 16,
        top: 0,
        left: 0,
        right: width,
        bottom: 16,
        x: 0,
        y: 0,
        toJSON: () => ({}),
      } as DOMRect;
    },
  );
}

function resizeTo(width: number) {
  availableWidth = width;
  act(() => {
    for (const cb of resizeCallbacks) cb();
  });
}

const finishedTurn = {
  durationMs: 92_400,
  model: "anthropic/claude-opus-4-1-20250805",
  promptTokens: 1_300_000,
  completionTokens: 86_400,
  cachedTokens: 41_000,
  firstTokenMs: 2100,
  tokensPerSec: 41,
};

function line() {
  return screen.getByTestId("message-meta-line");
}

describe("assistant message meta, compact density", () => {
  beforeEach(() => {
    availableWidth = 1000;
    stubLayout();
  });
  afterEach(() => {
    vi.useRealTimers();
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("Given compact density, When a finished turn renders, Then the line is abbreviated and first token stays out of it", () => {
    renderRow(assistantRow(finishedTurn), {}, { metaDensity: "compact" });

    const visible = within(line()).getByTestId("message-meta-visible");
    expect(visible.textContent).toContain("opus-4-1");
    expect(visible.textContent).not.toContain("anthropic/");
    expect(visible.textContent).not.toContain("20250805");
    expect(visible.textContent).toContain("1.3M");
    expect(visible.textContent).toContain("86.4k");
    expect(visible.textContent).toContain("1m32s");
    expect(visible.textContent).toContain("41 tok/s");
    expect(visible.textContent).not.toContain("2.1s");
    expect(screen.queryByTestId("message-first-token")).toBeNull();
    expect(screen.queryByTestId("message-meta-more")).toBeNull();
  });

  it("Given compact density, When rendered, Then copy is a 44px icon-only button", () => {
    renderRow(assistantRow(finishedTurn), {}, { metaDensity: "compact" });

    const copy = screen.getByRole("button", { name: "Copy AI output" });
    expect(copy.textContent).toBe("");
    expect(copy.className).toContain("size-11");
  });

  it("Given the line narrows, When it is re-fitted, Then tok/s goes first, then the duration, then the model is truncated, with a more mark", () => {
    renderRow(assistantRow(finishedTurn), {}, { metaDensity: "compact" });
    // 1m32s 带分隔点 = 6 字 → 60；全露需要 70 + 110 + 60 + 60 = 300。
    resizeTo(300);
    const visible = () =>
      within(line()).getByTestId("message-meta-visible").textContent ?? "";
    expect(visible()).toContain("tok/s");
    expect(screen.queryByTestId("message-meta-more")).toBeNull();

    resizeTo(299);
    expect(visible()).not.toContain("tok/s");
    expect(visible()).toContain("1m32s");
    expect(screen.getByTestId("message-meta-more").textContent).toBe("›");

    resizeTo(250);
    expect(visible()).not.toContain("1m32s");
    expect(visible()).toContain("1.3M");
    expect(screen.getByTestId("message-meta-model").style.minWidth).toBe("");

    resizeTo(150);
    const model = screen.getByTestId("message-meta-model");
    expect(model.textContent).toBe("opus-4-1");
    expect(model.style.minWidth).toBe("50px");
    expect(model.className).toContain("truncate");
    expect(visible()).toContain("86.4k");
    expect(screen.getByTestId("message-meta-more")).toBeDefined();

    resizeTo(1000);
    expect(visible()).toContain("tok/s");
    expect(screen.queryByTestId("message-meta-more")).toBeNull();
  });

  it("Given a running turn, When its duration grows past the room, Then the line is re-fitted on refresh", () => {
    vi.useFakeTimers();
    vi.setSystemTime(10_000);
    availableWidth = 220;
    renderRow(
      assistantRow({ durationMs: 0, model: "claude-sonnet-4-5" }),
      {
        showIndicator: true,
        liveTurn: {
          startedAt: 1000,
          firstTokenAt: 1500,
          generationMs: 0,
          burstStartedAt: null,
          promptTokens: 52_380,
          completionTokens: 1630,
          cachedTokens: 0,
          cacheCreationTokens: 0,
          reasoningTokens: 0,
          model: "claude-sonnet-4-5",
          liveText: "",
        },
      },
      { metaDensity: "compact" },
    );
    const visible = () =>
      within(line()).getByTestId("message-meta-visible").textContent ?? "";
    // ·9s = 3 字 → 30：70 + 110 + 30 = 210，放得下。
    expect(visible()).toContain("9s");
    expect(screen.queryByTestId("message-meta-more")).toBeNull();

    // ·1m09s = 6 字 → 60：240 放不下，用时让出去。
    act(() => {
      vi.advanceTimersByTime(60_000);
    });
    expect(visible()).not.toContain("1m09s");
    expect(screen.getByTestId("message-meta-more")).toBeDefined();
  });

  it("Given compact density, When the line is tapped, Then the this-turn sheet lists the full numbers including first token", () => {
    renderRow(assistantRow(finishedTurn), {}, { metaDensity: "compact" });

    fireEvent.click(line());

    const sheet = screen.getByRole("dialog");
    expect(within(sheet).getByText("This turn")).toBeDefined();
    expect(
      within(sheet).getByText("anthropic/claude-opus-4-1-20250805"),
    ).toBeDefined();
    expect(within(sheet).getByText("1,300,000")).toBeDefined();
    expect(within(sheet).getByText("86,400")).toBeDefined();
    expect(within(sheet).getByText("41,000")).toBeDefined();
    expect(within(sheet).getByText("First token")).toBeDefined();
    expect(within(sheet).getByText("2.1s")).toBeDefined();
    expect(within(sheet).getByText("1m 32s")).toBeDefined();
    expect(within(sheet).getByText("41 tok/s")).toBeDefined();
  });

  it("Given compact density, When copy is tapped, Then the sheet does not open", () => {
    renderRow(assistantRow(finishedTurn), {}, { metaDensity: "compact" });

    fireEvent.click(screen.getByRole("button", { name: "Copy AI output" }));

    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("Given the default density, When the meta is clicked, Then no sheet opens and nothing is abbreviated", () => {
    renderRow(assistantRow(finishedTurn));

    fireEvent.click(
      screen.getByRole("button", { name: "Token usage details" }),
    );

    expect(screen.queryByRole("dialog")).toBeNull();
    expect(screen.queryByTestId("message-meta-line")).toBeNull();
    expect(screen.queryByTestId("message-meta-more")).toBeNull();
    expect(
      screen.getByText("anthropic/claude-opus-4-1-20250805"),
    ).toBeDefined();
    expect(screen.getByTestId("message-token-counts").textContent).toContain(
      "1,300,000",
    );
    expect(screen.getByText("1m 32s")).toBeDefined();
    expect(
      screen.getByRole("button", { name: "Copy AI output" }).textContent,
    ).toBe("Copy");
  });
});

describe("user message alignment", () => {
  function userRow(isFirstOfMessage = true): TranscriptRow {
    const row = assistantRow({ role: "user", durationMs: 0 });
    return { ...row, isFirstOfMessage } as TranscriptRow;
  }

  it("Given userMessageAlign end, When a user message renders, Then its row is end-aligned", () => {
    const { container } = renderRow(userRow(), {}, { userMessageAlign: "end" });

    expect(container.querySelector("article")?.getAttribute("data-align")).toBe(
      "end",
    );
  });

  it("Given userMessageAlign end, When a later slice of a user message renders, Then that slice is end-aligned too", () => {
    const { container } = renderRow(
      userRow(false),
      {},
      {
        userMessageAlign: "end",
      },
    );

    expect(container.querySelector("[data-align='end']")).not.toBeNull();
  });

  it("Given userMessageAlign end, When an assistant message renders, Then it keeps the default alignment", () => {
    const { container } = renderRow(
      assistantRow({}),
      {},
      {
        userMessageAlign: "end",
      },
    );

    expect(container.querySelector("[data-align]")).toBeNull();
  });

  it("Given no alignment, When a user message renders, Then the row has no alignment marker", () => {
    const { container } = renderRow(userRow());

    expect(container.querySelector("[data-align]")).toBeNull();
  });
});
