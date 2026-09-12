import { render, screen } from "@testing-library/react";
import i18next from "i18next";
import { describe, expect, it } from "vitest";

import { AGENTRE_UI_NAMESPACE } from "../i18n";
import { TooltipProvider } from "../ui/tooltip";
import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";

import type { TranscriptRow } from "./transcript-rows";

const i18n = {
  t: (key: string) => i18next.t(key, { ns: AGENTRE_UI_NAMESPACE }),
};

// 非用户发起的一轮在转录结构上只有一种长相(这条 assistant 前面没有 user 消息),
// 但成因不止一种。分辨只能靠消息自己带的 turnTrigger —— 行渲染必须把它交给分隔卡,
// 否则外部唤醒的一轮会被谎报成「后台任务完成」(sess-3797)。
function autonomousRow(turnTrigger?: string): TranscriptRow {
  return {
    key: "message:9:notice:0",
    messageId: 9,
    message: {
      id: 9,
      role: "assistant",
      blocks: [{ type: "notice", text: "继续分派其余审查 agent。" }],
      createtime: 0,
      durationMs: 0,
      model: "",
      promptTokens: 0,
      completionTokens: 0,
      cachedTokens: 0,
      cacheCreationTokens: 0,
      reasoningTokens: 0,
      turnTrigger,
    },
    item: {
      type: "notice",
      uiStateKey: "message:9:notice:0",
      block: { type: "notice", text: "继续分派其余审查 agent。" },
    },
    isFirstOfMessage: true,
    isLastOfMessage: true,
    autonomous: true,
  } as unknown as TranscriptRow;
}

function renderRow(row: TranscriptRow) {
  render(
    <TooltipProvider>
      <TranscriptRenderContext.Provider
        value={{ agentName: "Agentre", agentAvatar: <span />, sessionId: 42 }}
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

describe("autonomous turn banner on a transcript row", () => {
  it("外部唤醒的一轮说的是外部唤醒", () => {
    renderRow(autonomousRow("external"));

    expect(
      screen.getByText(i18n.t("chatPanel.autonomous.externalBanner")),
    ).toBeDefined();
    expect(
      screen.queryByText(i18n.t("chatPanel.autonomous.banner")),
    ).toBeNull();
  });

  it("没有 turnTrigger 的历史消息仍是既有那句 —— 老转录逐字不变", () => {
    renderRow(autonomousRow(undefined));

    expect(
      screen.getByText(i18n.t("chatPanel.autonomous.banner")),
    ).toBeDefined();
  });
});
