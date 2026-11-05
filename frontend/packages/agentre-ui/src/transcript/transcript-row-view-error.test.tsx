import { fireEvent, render, screen, within } from "@testing-library/react";
import i18next from "i18next";
import { describe, expect, it, vi } from "vitest";

import { AGENTRE_UI_NAMESPACE } from "../i18n";
import { TooltipProvider } from "../ui/tooltip";
import {
  TranscriptRenderContext,
  type TranscriptRenderContextValue,
  TranscriptRowView,
} from "./transcript-row-view";
import type { TranscriptRow } from "./transcript-rows";

// 期望文案按 key 算：包测试独立跑与跑在宿主 vitest 里，默认语言不同。
const t = (key: string, opts?: Record<string, unknown>) =>
  i18next.t(key, { ...opts, ns: AGENTRE_UI_NAMESPACE });

const OVERLOADED =
  'API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}';
const AUTH_EXPIRED = "Failed to authenticate. API Error: 403";
const PROCESS_EXITED = "codex: process exited unexpectedly";
// internal/pkg/code 的 ChatRemoteRunInterrupted 中英两句（远端 agentred 重启打断了这一轮）。
const REMOTE_INTERRUPTED_ZH =
  "本轮在这里被打断了：远端 agentred 重启，这条会话已中断，不是运行失败。以上内容已保留，直接发消息即可开始新的一轮";
const REMOTE_INTERRUPTED_EN =
  "This turn was interrupted here: the remote agentred restarted and the session was cut short — it did not fail. Everything above is kept; send a message to start a new turn.";

function failedRow(
  errorText: string,
  meta: { durationMs?: number; model?: string } = {},
): TranscriptRow {
  return {
    key: "message:9:placeholder",
    messageId: 9,
    message: {
      id: 9,
      role: "assistant",
      blocks: [],
      createtime: 0,
      durationMs: meta.durationMs ?? 0,
      model: meta.model ?? "",
      promptTokens: 0,
      completionTokens: 0,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
      errorText,
    },
    item: { type: "placeholder" },
    isFirstOfMessage: true,
    isLastOfMessage: true,
    autonomous: false,
  } as unknown as TranscriptRow;
}

function renderFailed(
  row: TranscriptRow,
  ctx: Partial<TranscriptRenderContextValue> = {},
) {
  render(
    <TooltipProvider>
      <TranscriptRenderContext.Provider
        value={{
          agentName: "Agentre",
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
        />
      </TranscriptRenderContext.Provider>
    </TooltipProvider>,
  );
  return screen.getByTestId("turn-error-card");
}

function buttonNames(scope: HTMLElement): string[] {
  return within(scope)
    .queryAllByRole("button")
    .map((button) => button.textContent?.trim() ?? "");
}

describe("ErrorCard · fact title and collapsed details", () => {
  it("Given an overloaded errorText, When rendered, Then the card states the Chinese fact and keeps the raw text out of view until 详情 is opened", () => {
    const card = renderFailed(failedRow(OVERLOADED));

    expect(
      within(card).getByText(t("chat.errorCard.fact.overloaded")),
    ).toBeInTheDocument();
    expect(within(card).queryByText(OVERLOADED)).toBeNull();

    const toggle = within(card).getByRole("button", {
      name: t("chat.errorCard.details"),
    });
    expect(toggle).toHaveAttribute("aria-expanded", "false");

    fireEvent.click(toggle);

    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const raw = within(card).getByText(OVERLOADED);
    expect(raw).toHaveClass("font-mono");
    expect(
      within(card).getByRole("button", {
        name: t("chat.errorCard.copyDetails"),
      }),
    ).toBeInTheDocument();
  });
});

describe("ErrorCard · exits per failure kind", () => {
  it("Given a transient failure and a retry handler, When 重试这一轮 is clicked, Then the host retries that message and the card offers nothing else", () => {
    const onRetryTurn = vi.fn();
    const onRerun = vi.fn();
    const onContinue = vi.fn();
    const card = renderFailed(failedRow(OVERLOADED), {
      onRetryTurn,
      onRerun,
      onContinue,
    });

    expect(buttonNames(card)).toEqual([
      t("chat.errorCard.retryTurn"),
      t("chat.errorCard.details"),
    ]);
    fireEvent.click(
      within(card).getByRole("button", { name: t("chat.errorCard.retryTurn") }),
    );
    expect(onRetryTurn).toHaveBeenCalledWith(9);
    expect(onRerun).not.toHaveBeenCalled();
  });

  it("Given a transient failure but a host without a retry handler, When rendered, Then the card falls back to 继续 and 重新生成", () => {
    const onRerun = vi.fn();
    const onContinue = vi.fn();
    const card = renderFailed(failedRow(OVERLOADED), { onRerun, onContinue });

    expect(buttonNames(card)).toEqual([
      t("chat.errorCard.continue"),
      t("chat.actions.regenerate"),
      t("chat.errorCard.details"),
    ]);
  });

  it.each([
    ["claudecode", "claude /login"],
    ["codex", "codex login"],
  ])(
    "Given an expired login on a %s session, When rendered, Then the only exit is a command card with %s",
    (backendType, command) => {
      const card = renderFailed(failedRow(AUTH_EXPIRED), {
        backendType,
        onRetryTurn: vi.fn(),
        onRerun: vi.fn(),
        onContinue: vi.fn(),
      });

      expect(
        within(card).getByText(t("chat.errorCard.fact.authExpired")),
      ).toBeInTheDocument();
      expect(within(card).getByText(command)).toBeInTheDocument();
      expect(
        within(card).queryByRole("button", {
          name: t("chat.errorCard.retryTurn"),
        }),
      ).toBeNull();
      expect(
        within(card).queryByRole("button", {
          name: t("chat.errorCard.continue"),
        }),
      ).toBeNull();
      expect(
        within(card).queryByRole("button", {
          name: t("chat.actions.regenerate"),
        }),
      ).toBeNull();
    },
  );

  it("Given an expired login on a backend without a known login command, When rendered, Then the card shows only the fact and 详情", () => {
    const card = renderFailed(failedRow(AUTH_EXPIRED), {
      backendType: "hermes",
      onRetryTurn: vi.fn(),
      onRerun: vi.fn(),
      onContinue: vi.fn(),
    });

    expect(within(card).queryByRole("group")).toBeNull();
    expect(buttonNames(card)).toEqual([t("chat.errorCard.details")]);
  });

  it("Given a process that exited, When 继续 and 重新生成 are clicked, Then each reaches the host with the message id", () => {
    const onRerun = vi.fn();
    const onContinue = vi.fn();
    const card = renderFailed(failedRow(PROCESS_EXITED), {
      onRetryTurn: vi.fn(),
      onRerun,
      onContinue,
    });

    expect(
      within(card).getByText(t("chat.errorCard.fact.processExited")),
    ).toBeInTheDocument();
    fireEvent.click(
      within(card).getByRole("button", { name: t("chat.errorCard.continue") }),
    );
    fireEvent.click(
      within(card).getByRole("button", { name: t("chat.actions.regenerate") }),
    );
    expect(onContinue).toHaveBeenCalledWith(9);
    expect(onRerun).toHaveBeenCalledWith(9);
  });

  it.each([REMOTE_INTERRUPTED_ZH, REMOTE_INTERRUPTED_EN])(
    "Given a remote run that was interrupted (%s), When rendered, Then the visible title says the turn was interrupted — not that the process exited — and offers 继续 and 重新生成",
    (errorText) => {
      const onRerun = vi.fn();
      const onContinue = vi.fn();
      const card = renderFailed(failedRow(errorText), {
        onRetryTurn: vi.fn(),
        onRerun,
        onContinue,
      });

      const title = within(card).getByText(
        t("chat.errorCard.fact.interrupted"),
      );
      expect(title.textContent).toMatch(/interrupted|中断/i);
      expect(
        within(card).queryByText(t("chat.errorCard.fact.processExited")),
      ).toBeNull();
      expect(buttonNames(card)).toEqual([
        t("chat.errorCard.continue"),
        t("chat.actions.regenerate"),
        t("chat.errorCard.details"),
      ]);
      fireEvent.click(
        within(card).getByRole("button", {
          name: t("chat.errorCard.continue"),
        }),
      );
      expect(onContinue).toHaveBeenCalledWith(9);
    },
  );

  it("Given the footer already offers 重新生成, When an unknown failure renders, Then the card keeps 继续 and the page shows 重新生成 once", () => {
    const card = renderFailed(
      failedRow("API error 500: upstream exploded", {
        durationMs: 12_400,
        model: "claude-sonnet-4-5",
      }),
      { onRerun: vi.fn(), onContinue: vi.fn() },
    );

    expect(
      within(card).getByText(t("chat.errorCard.fact.unknown")),
    ).toBeInTheDocument();
    expect(buttonNames(card)).toEqual([
      t("chat.errorCard.continue"),
      t("chat.errorCard.details"),
    ]);
    expect(
      screen.getAllByRole("button", { name: t("chat.actions.regenerate") }),
    ).toHaveLength(1);
  });

  it("Given a read-only host with no handlers, When a process-exit failure renders, Then the card shows the fact and 详情 without action buttons", () => {
    const card = renderFailed(failedRow(PROCESS_EXITED));

    expect(buttonNames(card)).toEqual([t("chat.errorCard.details")]);
  });
});
