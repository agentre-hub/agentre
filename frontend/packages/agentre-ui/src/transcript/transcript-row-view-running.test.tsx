import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";

import { TooltipProvider } from "../ui/tooltip";

import type { TranscriptBlock, TranscriptMessage } from "./dto";
import {
  TranscriptRenderContext,
  TranscriptRowView,
} from "./transcript-row-view";
import { buildTranscriptRows, type TranscriptRow } from "./transcript-rows";
import { TranscriptUIStateProvider } from "./transcript-ui-state";

// 「这一轮在不在跑」的判据。
//
// 活动块的运行态(自动展开 + 超阈值省略中段)过去由 `liveBlocks !== undefined ||
// liveTail.length > 0` 反推 —— 那是「宿主有没有喂还没落库的内容」,不是「这一轮
// 在不在跑」。桌面端恰好恒真(store 的 LiveStream 永远带 liveBlocks 数组),所以
// 它一直是对的;agentre-server 控制台没有「已落库 / 未落库」这条分界(中继事件流
// 到一条画一条,liveBlocks 恒为 undefined),而正文不在流时 liveTail 也是空 ——
// 两个条件同时为假,运行态在控制台一次都没生效过。
//
// 这里钉住的是新契约:宿主可以直接说出这一轮在不在跑(`turnRunning`),不传时才
// 退回旧判据(消费方 pin 新版本之前行为不变)。

function toolUse(id: string, path: string): TranscriptBlock {
  return {
    type: "tool_use",
    toolUseId: id,
    toolName: "Read",
    toolInput: { path },
  };
}

function toolResult(id: string, text: string): TranscriptBlock {
  return { type: "tool_result", toolUseId: id, text };
}

/** 一条只含「连续三次只读探查」的 assistant —— 恰好聚合成一个多步活动块。 */
function assistantWithActivity(): TranscriptMessage {
  return {
    id: 2,
    sessionId: 1,
    role: "assistant",
    blocks: [
      toolUse("toolu-1", "/repo/a.go"),
      toolResult("toolu-1", "package a"),
      toolUse("toolu-2", "/repo/b.go"),
      toolResult("toolu-2", "package b"),
      toolUse("toolu-3", "/repo/c.go"),
      toolResult("toolu-3", "package c"),
    ],
    model: "",
    promptTokens: 0,
    completionTokens: 0,
    cachedTokens: 0,
    cacheCreationTokens: 0,
    reasoningTokens: 0,
    totalInputTokens: 0,
    durationMs: 0,
    errorText: "",
    seq: 1,
    createtime: 0,
  };
}

function activityRow(): TranscriptRow {
  const { rows } = buildTranscriptRows({
    displayMessages: [assistantWithActivity()],
    autonomousIds: new Set<number>(),
  });
  const row = rows.find((r) => r.item.type === "activity");
  if (!row) throw new Error("expected an activity row");
  return row;
}

function renderRow(props: {
  liveBlocks?: TranscriptBlock[];
  liveTail?: string;
  turnRunning?: boolean;
}) {
  render(
    <TooltipProvider>
      <TranscriptUIStateProvider>
        <TranscriptRenderContext.Provider
          value={{ agentName: "Agentre", agentAvatar: <span />, sessionId: 42 }}
        >
          <TranscriptRowView
            row={activityRow()}
            liveTail={props.liveTail ?? ""}
            liveBlocks={props.liveBlocks}
            liveRetry={null}
            showIndicator={false}
            compacting={false}
            reconnecting={false}
            turnRunning={props.turnRunning}
          />
        </TranscriptRenderContext.Provider>
      </TranscriptUIStateProvider>
    </TooltipProvider>,
  );
}

describe("TranscriptRowView 的运行态判据", () => {
  it("Given 控制台的喂法(liveBlocks 未定义、liveTail 为空), When 宿主明说这一轮在跑, Then 活动块自动展开", () => {
    // 这是 agentre-server 控制台的真实形态:它没有未落库那一段可喂,只有一个
    // 如实的生命周期信号。旧判据下它恒为假,活动块永远收着。
    renderRow({ turnRunning: true });

    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });

  it("Given 宿主明说这一轮已经落定, When 它仍在喂 liveBlocks, Then 活动块收起(如实信号压过旧判据)", () => {
    // 新入参是「如实」的那一个:它一旦给出,就不再从「有没有未落库内容」反推。
    renderRow({ liveBlocks: [], turnRunning: false });

    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });

  it("Given 桌面端的旧喂法(liveBlocks 是数组、不传新入参), When 渲染, Then 活动块仍自动展开", () => {
    // 向后兼容:消费方 pin 到新版本之前不能行为突变。
    renderRow({ liveBlocks: [] });

    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "true",
    );
  });

  it("Given 已落定的历史消息(既无 live 内容也不传新入参), When 渲染, Then 活动块保持收起", () => {
    renderRow({});

    expect(screen.getByTestId("activity-header")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });
});
