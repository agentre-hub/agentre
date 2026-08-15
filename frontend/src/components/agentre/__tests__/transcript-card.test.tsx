import { render, screen } from "@testing-library/react";
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import {
  TranscriptCard,
  TranscriptCardBody,
  TranscriptCardHeader,
  TranscriptPill,
  ThinkingBlock,
  CompactBoundaryDivider,
} from "@agentre-ai/agentre-ui";
import { AutoTriggerBanner } from "../auto-trigger-banner";
import { CompactHistoryFold } from "../compact-history-fold";
import { TranscriptRowView } from "../transcript-row-view";

import type { TranscriptRow } from "@agentre-ai/agentre-ui";
import type { chat_svc } from "../../../../wailsjs/go/models";

describe("TranscriptCard", () => {
  it("默认 tone 用中性边框 + measure 栏宽 + 无阴影", () => {
    render(<TranscriptCard data-testid="c">x</TranscriptCard>);
    const el = screen.getByTestId("c");
    expect(el.className).toContain("max-w-measure");
    expect(el.className).toContain("rounded-lg");
    expect(el.className).toContain("bg-card");
    expect(el.className).toContain("border-border");
    expect(el.className).not.toContain("shadow");
  });

  it("error tone 换成告警边框", () => {
    render(
      <TranscriptCard tone="error" data-testid="c">
        x
      </TranscriptCard>,
    );
    const el = screen.getByTestId("c");
    expect(el.className).toContain("border-status-error/40");
    expect(el.className).not.toContain("border-border");
  });

  it("调用方 className 能覆盖默认值", () => {
    render(
      <TranscriptCard className="rounded-none" data-testid="c">
        x
      </TranscriptCard>,
    );
    expect(screen.getByTestId("c").className).toContain("rounded-none");
    expect(screen.getByTestId("c").className).not.toContain("rounded-lg");
  });

  it("header 与 body 用同一套水平内边距", () => {
    render(
      <TranscriptCard>
        <TranscriptCardHeader data-testid="h">h</TranscriptCardHeader>
        <TranscriptCardBody data-testid="b">b</TranscriptCardBody>
      </TranscriptCard>,
    );
    expect(screen.getByTestId("h").className).toContain("px-3.5");
    expect(screen.getByTestId("b").className).toContain("px-3.5");
    expect(screen.getByTestId("b").className).toContain("border-t");
  });

  it("pill 用 text-meta,不再是 9px", () => {
    render(<TranscriptPill data-testid="p">完成</TranscriptPill>);
    const el = screen.getByTestId("p");
    expect(el.className).toContain("text-meta");
    expect(el.className).not.toMatch(/text-\[\d+px\]/);
  });

  it("done tone 的 pill 用 running 配色", () => {
    render(
      <TranscriptPill tone="done" data-testid="p">
        完成
      </TranscriptPill>,
    );
    expect(screen.getByTestId("p").className).toContain("bg-status-running-bg");
    expect(screen.getByTestId("p").className).toContain("text-status-running");
  });
});

describe("ThinkingBlock 排版契约", () => {
  it("即使脱离 MessageRow 也受 measure 约束", () => {
    const { container } = render(
      <ThinkingBlock text="思考内容" streaming={false} />,
    );
    const root = container.firstElementChild as HTMLElement;
    expect(root.className).toContain("max-w-measure");
  });
});

describe("脱离 MessageRow 渲染的块也守 measure", () => {
  it("CompactBoundaryDivider 受 measure 约束", () => {
    const { container } = render(<CompactBoundaryDivider at={0} />);
    const root = container.firstElementChild as HTMLElement;
    expect(root.className).toContain("max-w-measure");
  });

  it("CompactHistoryFold 受 measure 约束", () => {
    const { container } = render(
      <CompactHistoryFold count={3} onExpand={() => {}} />,
    );
    const root = container.firstElementChild as HTMLElement;
    expect(root.className).toContain("max-w-measure");
  });

  it("AutoTriggerBanner 受 measure 约束", () => {
    const { container } = render(<AutoTriggerBanner />);
    const root = container.firstElementChild as HTMLElement;
    expect(root.className).toContain("max-w-measure");
  });
});

// ─── R13:断连期间的活信号 ────────────────────────────────────────────────────
//
// 断连指示器是 TypingIndicator / CompactingIndicator 的第三个兄弟:同一组点、
// 同一 typing-dot 曲线、周期慢一倍 + 断线图形 + 一行内联短标签。它换掉打字指示器,
// 出现在转录流里同一个位置,恢复连接立刻换回。测试语言是 en(setup.ts 固定)。
const TYPING_ARIA = "Generating";
const DISCONNECTED_ARIA = "Connection lost, reconnecting";
const DISCONNECTED_LABEL =
  "Connection lost, reconnecting · the remote is still running";

function indicatorHostRow(): TranscriptRow {
  return {
    key: "row-1",
    messageId: 1,
    message: {
      blocks: [],
      completionTokens: 0,
      createtime: 0,
      durationMs: 0,
      errorText: "",
      id: 1,
      model: "",
      promptTokens: 0,
      role: "assistant",
      seq: 1,
      sessionId: 1,
    } as unknown as chat_svc.ChatMessage,
    item: { type: "placeholder" },
    isFirstOfMessage: false,
    isLastOfMessage: true,
    autonomous: false,
  };
}

function renderIndicatorHost(opts: {
  reconnecting: boolean;
  compacting?: boolean;
}) {
  return render(
    <TranscriptRowView
      row={indicatorHostRow()}
      liveTail=""
      liveBlocks={undefined}
      liveRetry={null}
      showIndicator
      compacting={opts.compacting ?? false}
      reconnecting={opts.reconnecting}
    />,
  );
}

describe("断连期间的活信号 (R13)", () => {
  it("重连态渲染断连形态而非打字形态", () => {
    renderIndicatorHost({ reconnecting: true });
    expect(screen.queryByLabelText(TYPING_ARIA)).toBeNull();
    const indicator = screen.getByLabelText(DISCONNECTED_ARIA);
    // 短标签就在指示器内联,不是横幅:与点同处一行,随转录流滚动。
    expect(indicator).toHaveTextContent(DISCONNECTED_LABEL);
    expect(indicator.getAttribute("role")).toBe("status");
  });

  it("连接恢复后立刻换回打字形态", () => {
    const { rerender } = renderIndicatorHost({ reconnecting: true });
    expect(screen.getByLabelText(DISCONNECTED_ARIA)).toBeTruthy();
    rerender(
      <TranscriptRowView
        row={indicatorHostRow()}
        liveTail=""
        liveBlocks={undefined}
        liveRetry={null}
        showIndicator
        compacting={false}
        reconnecting={false}
      />,
    );
    expect(screen.queryByLabelText(DISCONNECTED_ARIA)).toBeNull();
    expect(screen.getByLabelText(TYPING_ARIA)).toBeTruthy();
  });

  it("断连形态压过压缩形态:通道断了就先说通道", () => {
    renderIndicatorHost({ reconnecting: true, compacting: true });
    expect(screen.queryByLabelText("Compacting context")).toBeNull();
    expect(screen.getByLabelText(DISCONNECTED_ARIA)).toBeTruthy();
  });

  it("点沿用同一尺寸与曲线,只换慢一倍的周期,并带 reduced-motion 降级", () => {
    renderIndicatorHost({ reconnecting: true });
    const dotGroup = screen.getByLabelText(DISCONNECTED_ARIA)
      .firstElementChild as HTMLElement;
    const dots = Array.from(dotGroup.children) as HTMLElement[];
    expect(dots).toHaveLength(3);
    for (const dot of dots) {
      expect(dot.className).toContain("size-1.5");
      expect(dot.className).toContain("animate-typing-dot-slow");
      // 降级:停动画 + 静态低透明度,信息不丢失。
      expect(dot.className).toContain("motion-reduce:animate-none");
      expect(dot.className).toContain("motion-reduce:opacity-55");
    }
  });

  it("三点的相位延迟随周期一同翻倍,三点间的相位差才是同一条曲线", () => {
    // 相位延迟是曲线的一部分:周期翻倍而延迟不动,三点会挤成几乎同相地一起呼吸,
    // 那是另一条曲线,不是「同一曲线放慢一倍」(R13)。
    const delaysOf = (dots: HTMLElement[]): number[] =>
      dots.map((dot) => {
        const m = /\[animation-delay:([\d.]+)s\]/.exec(dot.className);
        return m ? Number(m[1]) : 0;
      });

    const typing = renderIndicatorHost({ reconnecting: false });
    const fast = delaysOf(
      Array.from(screen.getByLabelText(TYPING_ARIA).children) as HTMLElement[],
    );
    typing.unmount();

    renderIndicatorHost({ reconnecting: true });
    const slow = delaysOf(
      Array.from(
        (
          screen.getByLabelText(DISCONNECTED_ARIA)
            .firstElementChild as HTMLElement
        ).children,
      ) as HTMLElement[],
    );

    expect(fast).toHaveLength(3);
    // 打字形态本来就有相位差(0 / 0.15 / 0.3),否则下面的等式在全 0 时也成立。
    expect(fast.filter((d) => d > 0)).toHaveLength(2);
    expect(slow).toEqual(fast.map((d) => d * 2));
  });

  it("慢一倍是 @theme 注册的同一 keyframe,不是另起一套动效", () => {
    // 动效 token 与 keyframe 已随 design tokens 抽到共享包,globals.css 只剩 @import。
    const css = fs.readFileSync(
      path.resolve(process.cwd(), "packages/agentre-ui/styles/tokens.css"),
      "utf8",
    );
    const shape = /typing-dot\s+([\d.]+)s\s+([a-z-]+)\s+infinite/;
    const base = shape.exec(
      /--animate-typing-dot:([^;]+);/.exec(css)?.[1] ?? "",
    );
    const slow = shape.exec(
      /--animate-typing-dot-slow:([^;]+);/.exec(css)?.[1] ?? "",
    );
    expect(base).not.toBeNull();
    expect(slow).not.toBeNull();
    expect(Number(slow![1])).toBeCloseTo(Number(base![1]) * 2);
    expect(slow![2]).toBe(base![2]);
  });
});
