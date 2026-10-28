import { cleanup, render, screen } from "@testing-library/react";
import i18next from "i18next";
import { afterEach, describe, expect, it } from "vitest";

import {
  EventDone,
  EventTextDelta,
  EventUnrecognizedBlock,
  EventUserMessage,
} from "../event-kinds.gen";
import { AGENTRE_UI_NAMESPACE } from "../i18n";
import { TooltipProvider } from "../ui/tooltip";
import { reduceFrames, type TranscriptFrame } from "./frames";
import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";
import type { TranscriptRow } from "./transcript-rows";

/**
 * 会话设置切换 notice（换供应商 / 换推理强度 / 换 CLI 模型）在拿帧渲染的那两个面
 * （agentre-server 控制台、桌面端 Peer Tab）上的形状。
 *
 * 桌面端 `chat_svc.appendSessionNotice` 把它落成一条只有一个 `notice` 块的 assistant
 * 消息，结构化负载是编码进 `NoticeBlock.Text` 的小 JSON（`view.ProviderNotice`）。
 * 折帧时 `transcript.EventForStoredBlock` 只认 Hermes 那一种 notice，其余一律走
 * `UnrecognizedBlock` 兜底——所以浏览器收到的是块的**原始 JSON**：
 * `{kind:"unrecognized_block", blockType:"notice", data:{level, text:"<负载 JSON>"}}`。
 *
 * 桌面端自己的会话由 Go 的 `noticeBlockToChatBlock` 把负载解回 DTO 字段（Text 置空），
 * 这里断言的就是那份 DTO —— 同一条 notice 在两个面上必须是同一个块。
 */

const SID = 7;

function frame(event: Record<string, unknown>): TranscriptFrame {
  return { createtime: 0, event } as TranscriptFrame;
}

function storedNoticeFrame(text: string, level = "info") {
  return frame({
    kind: EventUnrecognizedBlock,
    blockType: "notice",
    data: { text, level },
  });
}

function onlyBlock(frames: TranscriptFrame[]) {
  const messages = reduceFrames(frames, SID);
  expect(messages).toHaveLength(1);
  expect(messages[0].role).toBe("assistant");
  expect(messages[0].blocks).toHaveLength(1);
  return messages[0].blocks[0];
}

const t = (key: string, opts?: Record<string, unknown>) =>
  i18next.t(key, { ...opts, ns: AGENTRE_UI_NAMESPACE });

afterEach(cleanup);

describe("落库的会话设置切换 notice 经帧归约成桌面端同一个块", () => {
  it("CLI 模型切到固定模型：解回 noticeKind / cliModelMode / cliModel，不留原始 JSON", () => {
    const block = onlyBlock([
      storedNoticeFrame(
        JSON.stringify({
          kind: "cli_model",
          cliModelMode: "fixed",
          cliModel: "sonnet",
        }),
      ),
    ]);
    expect(block).toEqual({
      type: "notice",
      level: "info",
      noticeKind: "cli_model",
      cliModelMode: "fixed",
      cliModel: "sonnet",
    });
  });

  it("CLI 模型改回跟随 Agent 绑定：mode 为空也认 kind，仍是结构化块", () => {
    const block = onlyBlock([
      storedNoticeFrame(JSON.stringify({ kind: "cli_model" })),
    ]);
    expect(block).toEqual({
      type: "notice",
      level: "info",
      noticeKind: "cli_model",
    });
  });

  it("轮末实际模型偏离：带 cliModel 与 actualModel", () => {
    const block = onlyBlock([
      storedNoticeFrame(
        JSON.stringify({
          kind: "cli_model_deviation",
          cliModel: "opus",
          actualModel: "sonnet",
        }),
        "warning",
      ),
    ]);
    expect(block).toEqual({
      type: "notice",
      level: "warning",
      noticeKind: "cli_model_deviation",
      cliModel: "opus",
      actualModel: "sonnet",
    });
  });

  it("推理强度切换：带 reasoningEffort", () => {
    const block = onlyBlock([
      storedNoticeFrame(
        JSON.stringify({ kind: "reasoning_effort", reasoningEffort: "high" }),
      ),
    ]);
    expect(block).toEqual({
      type: "notice",
      level: "info",
      noticeKind: "reasoning_effort",
      reasoningEffort: "high",
    });
  });

  it("供应商切换到固定模型：带供应商与模型的 key 和展示名", () => {
    const block = onlyBlock([
      storedNoticeFrame(
        JSON.stringify({
          providerKey: "acme",
          providerName: "Acme",
          modelKey: "m1",
          modelName: "Model One",
          kind: "switch",
        }),
      ),
    ]);
    expect(block).toEqual({
      type: "notice",
      level: "info",
      noticeKind: "switch",
      providerKey: "acme",
      providerName: "Acme",
      modelKey: "m1",
      modelName: "Model One",
    });
  });

  it("供应商回退（旧数据，无 kind 只有 providerKey）：仍是结构化块", () => {
    const block = onlyBlock([
      storedNoticeFrame(JSON.stringify({ providerKey: "gone" })),
    ]);
    expect(block).toEqual({
      type: "notice",
      level: "info",
      providerKey: "gone",
    });
  });

  it.each([
    ["自由文本", "Selected provider is unavailable"],
    ["既无 providerKey 也无 kind 的 JSON", JSON.stringify({ foo: "bar" })],
    ["JSON 但不是对象", JSON.stringify(["cli_model"])],
    ["字段类型不对", JSON.stringify({ kind: 5 })],
  ])("非结构化负载（%s）：与桌面端一样原样显示 text", (_label, text) => {
    const block = onlyBlock([storedNoticeFrame(text)]);
    expect(block).toEqual({ type: "notice", level: "info", text });
  });

  it("notice 停在它自己那条消息上：帧序里它在哪，转录里就在哪", () => {
    const messages = reduceFrames(
      [
        frame({ kind: EventUserMessage, text: "first" }),
        frame({ kind: EventTextDelta, text: "reply one" }),
        frame({ kind: EventDone }),
        storedNoticeFrame(
          JSON.stringify({
            kind: "cli_model",
            cliModelMode: "fixed",
            cliModel: "sonnet",
          }),
        ),
        frame({ kind: EventDone }),
        frame({ kind: EventUserMessage, text: "second" }),
        frame({ kind: EventTextDelta, text: "reply two" }),
        frame({ kind: EventDone }),
      ],
      SID,
    );
    expect(
      messages.map((m) => [
        m.role,
        m.blocks.map((b) => b.noticeKind ?? b.text),
      ]),
    ).toEqual([
      ["user", ["first"]],
      ["assistant", ["reply one"]],
      ["assistant", ["cli_model"]],
      ["user", ["second"]],
      ["assistant", ["reply two"]],
    ]);
  });

  it("渲染出来是桌面端那句切换文案，看不到负载 JSON", () => {
    const [message] = reduceFrames(
      [
        storedNoticeFrame(
          JSON.stringify({
            kind: "cli_model",
            cliModelMode: "fixed",
            cliModel: "sonnet",
          }),
        ),
      ],
      SID,
    );
    const row = {
      key: `message:${message.id}:notice:0`,
      messageId: message.id,
      message,
      item: {
        type: "notice",
        uiStateKey: `message:${message.id}:notice:0`,
        block: message.blocks[0],
      },
      isFirstOfMessage: true,
      isLastOfMessage: true,
      autonomous: false,
    } as unknown as TranscriptRow;
    render(
      <TooltipProvider>
        <TranscriptRenderContext.Provider
          value={{
            agentName: "Agentre",
            agentAvatar: <span />,
            sessionId: SID,
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
    const notice = screen.getByTestId("transcript-notice");
    expect(notice).toHaveTextContent(
      t("modelTargetPicker.cli.switchedFixed", { model: "sonnet" }),
    );
    expect(notice.textContent).not.toContain("cli_model");
  });
});
