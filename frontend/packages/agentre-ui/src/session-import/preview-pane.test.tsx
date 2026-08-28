import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { PreviewPane } from "./preview-pane";
import type { ImportPreviewResult } from "./ports";
import type { TranscriptMessage } from "../transcript/dto";

/**
 * 右栏（规格「预览」）。三条验收：
 *
 * 1. 预览用的是**真实转录渲染链**，不是第二个简化渲染器 —— 所以后端投影出来的
 *    工具卡、缺口说明块（`notice`）在这里就已经长成导进去之后的样子。
 * 2. 缺口在导入**前**说一次（G1，决策 11）。
 * 3. 末尾说清「后面还有多少轮」，别让人以为导的就是这几轮。
 */
function message(over: Partial<TranscriptMessage>): TranscriptMessage {
  return {
    id: 1,
    sessionId: 0,
    role: "user",
    blocks: [{ type: "text", text: "hello" }],
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
    createtime: 1_756_000_000_000,
    ...over,
  };
}

function result(over: Partial<ImportPreviewResult> = {}): ImportPreviewResult {
  return {
    meta: {
      backend: "claudecode",
      providerSessionId: "abc",
      title: "Refactor wire protocol",
      cwd: "/Code/agentre",
      model: "claude-opus-5",
      turns: 42,
      toolCalls: 402,
      compactions: 1,
      startedAt: 1_756_000_000_000,
      endedAt: 1_756_010_000_000,
      origin: "terminal",
      gaps: [],
      cwdExists: true,
      imported: false,
      importedSessionId: "",
      ...over.meta,
    },
    messages: over.messages ?? [
      message({ id: 1, role: "user" }),
      message({
        id: 2,
        role: "assistant",
        seq: 2,
        blocks: [{ type: "text", text: "on it" }],
      }),
    ],
    previewedTurns: over.previewedTurns ?? 1,
    remainingTurns: over.remainingTurns ?? 41,
  };
}

describe("转录预览栏", () => {
  it("Given 一份预览（含后端写进转录的缺口说明块）, When 渲染, Then 用户消息、助手正文与那条 notice 都按 agentre 自己的转录行长出来", () => {
    render(
      <PreviewPane
        state={{
          kind: "ready",
          result: result({
            messages: [
              message({ id: 1, role: "user" }),
              message({
                id: 2,
                role: "assistant",
                seq: 2,
                blocks: [
                  {
                    type: "notice",
                    level: "info",
                    text: "思维过程未随会话保存（claude 加密存储）",
                  },
                  { type: "text", text: "on it" },
                ],
              }),
            ],
          }),
        }}
        onOpenImported={vi.fn()}
      />,
    );

    const transcript = screen.getByTestId("import-preview-transcript");
    expect(transcript.textContent).toContain("hello");
    expect(transcript.textContent).toContain("on it");
    // G3：缺口在转录里就地说明 —— 不需要第二种块类型，`notice` 原样成行。
    expect(transcript.textContent).toContain("思维过程未随会话保存");
  });

  it("Given 这条转录带缺口声明, When 渲染, Then 预览区顶部先把缺什么、为什么缺说清（G1，决策点上）", () => {
    render(
      <PreviewPane
        state={{
          kind: "ready",
          result: result({
            meta: {
              ...result().meta,
              gaps: [
                {
                  kind: "thinking_unavailable",
                  count: 42,
                  detail: "claude-opus-5",
                  text: "这条会话的思维过程 claude 存的是加密内容",
                },
              ],
            },
          }),
        }}
        onOpenImported={vi.fn()}
      />,
    );

    expect(screen.getByTestId("import-preview-gaps").textContent).toContain(
      "这条会话的思维过程 claude 存的是加密内容",
    );
  });

  it("Given 预览只取了前几轮, When 渲染末尾, Then 说清导入后是完整多少轮", () => {
    render(
      <PreviewPane
        state={{ kind: "ready", result: result() }}
        onOpenImported={vi.fn()}
      />,
    );
    expect(screen.getByTestId("import-preview-tail").textContent).toContain(
      "42",
    );
  });

  it("Given 转录打不开（文件已删 / 损坏到解不出任何一轮）, When 渲染, Then 右栏给出原因而不是一片空白", () => {
    render(
      <PreviewPane
        state={{ kind: "error", message: "open transcript: no such file" }}
        onOpenImported={vi.fn()}
      />,
    );
    const box = screen.getByTestId("import-preview-error");
    expect(box.getAttribute("role")).toBe("alert");
    expect(box.textContent).toContain("open transcript: no such file");
  });

  it("Given cwd 已不存在, When 渲染, Then 说清「转录照导、续跑关掉」而不是假装能跑（决策 16）", () => {
    render(
      <PreviewPane
        state={{
          kind: "ready",
          result: result({ meta: { ...result().meta, cwdExists: false } }),
        }}
        onOpenImported={vi.fn()}
      />,
    );
    expect(screen.getByTestId("import-cwd-missing").textContent).toContain(
      "/Code/agentre",
    );
  });

  it("Given 选中项变化, When 右栏重画, Then 这一栏是个 aria-live 区域（键盘用户按方向键时右栏不能无声地换）", () => {
    render(<PreviewPane state={{ kind: "idle" }} onOpenImported={vi.fn()} />);
    const pane = screen.getByTestId("import-preview");
    expect(pane.getAttribute("aria-live")).toBe("polite");
    expect(pane.getAttribute("role")).toBe("region");
  });
});
