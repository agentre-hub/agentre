import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { ThinkingBlock } from "./thinking-block";

afterEach(() => {
  vi.useRealTimers();
});

function mockTextSelectionWithin(node: Node) {
  const range = { commonAncestorContainer: node } as Range;
  return vi.spyOn(window, "getSelection").mockReturnValue({
    anchorNode: node,
    focusNode: node,
    getRangeAt: () => range,
    isCollapsed: false,
    rangeCount: 1,
    toString: () => "selected",
  } as unknown as Selection);
}

describe("ThinkingBlock", () => {
  it("renders nothing when text is empty", () => {
    const { container } = render(<ThinkingBlock text="" streaming={false} />);
    expect(container.firstChild).toBeNull();
  });

  describe("streaming state", () => {
    it("marks thinking text as selectable/copyable", () => {
      render(<ThinkingBlock text="正在分析这个问题" streaming />);
      const label = screen.getByText("Thinking…");

      expect(label.closest("[data-selectable-text='true']")).not.toBeNull();
      expect(label).toHaveAttribute("data-copyable-control-text", "true");
      expect(screen.getByText("0s")).toHaveAttribute(
        "data-copyable-control-text",
        "true",
      );
    });

    it("shows '思考中…' header and the streaming content", () => {
      render(<ThinkingBlock text="正在分析这个问题" streaming />);
      expect(screen.getByText("Thinking…")).toBeInTheDocument();
      expect(screen.getByText("正在分析这个问题")).toBeInTheDocument();
    });

    it("shows a ticking seconds chip", () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date("2026-05-18T10:00:00Z"));
      render(<ThinkingBlock text="x" streaming />);
      expect(screen.getByText("0s")).toBeInTheDocument();
      act(() => {
        vi.advanceTimersByTime(3500);
      });
      expect(screen.getByText("3s")).toBeInTheDocument();
    });

    it("body is expanded (aria-expanded=true)", () => {
      render(<ThinkingBlock text="x" streaming />);
      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-expanded",
        "true",
      );
    });

    it("allows collapsing while streaming and toggling back", () => {
      render(<ThinkingBlock text="x" streaming />);
      const btn = screen.getByRole("button");
      expect(btn).toHaveAttribute("aria-expanded", "true");
      fireEvent.click(btn);
      expect(btn).toHaveAttribute("aria-expanded", "false");
      fireEvent.click(btn);
      expect(btn).toHaveAttribute("aria-expanded", "true");
    });

    it("does not collapse when the click is finishing a text selection", () => {
      render(<ThinkingBlock text="x" streaming />);
      const btn = screen.getByRole("button");
      const label = screen.getByText("Thinking…");
      const textNode = label.firstChild;
      if (!textNode) throw new Error("Expected thinking label text node");
      const selection = mockTextSelectionWithin(textNode);

      fireEvent.click(btn);

      expect(btn).toHaveAttribute("aria-expanded", "true");
      selection.mockRestore();
    });

    it("header is always cursor-pointer (no disabled while streaming)", () => {
      render(<ThinkingBlock text="x" streaming />);
      const btn = screen.getByRole("button");
      expect(btn).not.toBeDisabled();
      expect(btn.className).toContain("cursor-pointer");
      expect(btn.className).not.toContain("cursor-default");
    });
  });

  describe("done state (never streamed)", () => {
    it("shows '思考完成' header and char-count meta only", () => {
      render(<ThinkingBlock text="你好世界abc" streaming={false} />);
      expect(screen.getByText("Thought complete")).toBeInTheDocument();
      expect(screen.getByText("· 7 chars")).toBeInTheDocument();
    });

    it("collapsed by default", () => {
      render(<ThinkingBlock text="x" streaming={false} />);
      expect(screen.getByRole("button")).toHaveAttribute(
        "aria-expanded",
        "false",
      );
    });

    it("clicking toggles expanded ↔ collapsed", () => {
      const { container } = render(
        <ThinkingBlock text="x" streaming={false} />,
      );
      const btn = screen.getByRole("button");
      const content = container.querySelector(
        '[data-slot="thinking-block-content"]',
      );

      expect(content).toHaveAttribute("aria-hidden", "true");
      expect(content).toHaveClass("transition-[grid-template-rows]");
      expect(content).not.toHaveClass(
        "motion-safe:fade-in-0",
        "motion-safe:animate-in",
      );
      expect(content).toHaveStyle("grid-template-rows: 0fr");

      fireEvent.click(btn);
      expect(btn).toHaveAttribute("aria-expanded", "true");
      expect(content).toHaveAttribute("aria-hidden", "false");
      expect(content).toHaveStyle("grid-template-rows: 1fr");

      fireEvent.click(btn);
      expect(btn).toHaveAttribute("aria-expanded", "false");
      expect(content).toHaveAttribute("aria-hidden", "true");
      expect(content).toHaveStyle("grid-template-rows: 0fr");
    });
  });

  describe("streaming → done transition", () => {
    it("auto-collapses and records elapsed seconds in meta", () => {
      vi.useFakeTimers();
      vi.setSystemTime(new Date("2026-05-18T10:00:00Z"));
      const { rerender } = render(<ThinkingBlock text="abc" streaming />);
      act(() => {
        vi.advanceTimersByTime(12000);
      });
      rerender(<ThinkingBlock text="abc" streaming={false} />);
      const btn = screen.getByRole("button");
      expect(btn).toHaveAttribute("aria-expanded", "false");
      expect(screen.getByText("Thought complete")).toBeInTheDocument();
      expect(screen.getByText("· 12s · 3 chars")).toBeInTheDocument();
    });

    it("auto-collapses even when user expanded mid-stream", () => {
      const { rerender } = render(<ThinkingBlock text="abc" streaming />);
      const btn = screen.getByRole("button");
      // 默认展开 → 用户中途点击折叠 → 再点击展开,模拟"用户在 streaming 中拨弄过状态"
      fireEvent.click(btn);
      expect(btn).toHaveAttribute("aria-expanded", "false");
      fireEvent.click(btn);
      expect(btn).toHaveAttribute("aria-expanded", "true");
      rerender(<ThinkingBlock text="abc" streaming={false} />);
      expect(btn).toHaveAttribute("aria-expanded", "false");
    });

    it("does not reset expanded on unrelated rerenders after done", () => {
      const { rerender } = render(
        <ThinkingBlock text="abc" streaming={false} />,
      );
      const btn = screen.getByRole("button");
      fireEvent.click(btn);
      expect(btn).toHaveAttribute("aria-expanded", "true");
      rerender(<ThinkingBlock text="abc" streaming={false} />);
      expect(btn).toHaveAttribute("aria-expanded", "true");
    });
  });

  // 防御性能 bug:超长思考流时,streaming 视图若把整段 text 都塞进 DOM,每个 chunk
  // 都要 O(n) 重排 whitespace-pre-wrap 文本 → 整轮流式 O(n²) → 主线程卡死。streaming
  // body 本就 max-h-[132px] overflow-hidden 并贴底,头部根本看不见,所以只需要渲染
  // 末尾一段「尾巴」。这里锁定契约:streaming 时 DOM 里只含有界尾巴,头部 marker 不在;
  // done 时仍渲染完整文本(落库/展开要用)。
  describe("long streaming text renders only a bounded tail", () => {
    const headMarker = "XHEAD-MARKER-X";

    it("does not put the head / full text in the streaming DOM", () => {
      const longText = headMarker + "思".repeat(20000);
      const { container } = render(<ThinkingBlock text={longText} streaming />);
      const body = container.querySelector(
        '[data-slot="thinking-streaming-body"]',
      );
      expect(body).not.toBeNull();
      const rendered = body?.textContent ?? "";
      expect(rendered).not.toContain(headMarker);
      expect(rendered.length).toBeLessThan(longText.length);
      // 有界常数:不能只砍一半,得砍到一个固定窗口量级。
      expect(rendered.length).toBeLessThan(2000);
    });

    it("still renders short text in full while streaming", () => {
      render(<ThinkingBlock text="正在分析这个问题" streaming />);
      expect(screen.getByText("正在分析这个问题")).toBeInTheDocument();
    });

    it("renders the full text in the done state", () => {
      const longText = headMarker + "尾".repeat(20000);
      const { container } = render(
        <ThinkingBlock text={longText} streaming={false} />,
      );
      const content = container.querySelector(
        '[data-slot="thinking-block-content"]',
      );
      expect(content?.textContent ?? "").toContain(headMarker);
    });
  });

  // 防御真实 bug:Claude Code CLI 把 thinking 整段一次性发出来,合成块只活几 ms,
  // 自计时只能拿到 0s。让外部传入 stream 起始时刻,从「按发送」开始算才有意义。
  describe("external startedAt", () => {
    it("uses external startedAt for live seconds chip", () => {
      vi.useFakeTimers();
      const T0 = new Date("2026-05-18T10:00:00Z").getTime();
      // 模拟 stream 已经跑了 8 秒,这一刻 thinking 块刚挂出来
      vi.setSystemTime(T0 + 8000);
      render(<ThinkingBlock text="x" streaming startedAt={T0} />);
      expect(screen.getByText("8s")).toBeInTheDocument();
    });

    it("captures elapsed since external startedAt when streaming flips off", () => {
      vi.useFakeTimers();
      const T0 = new Date("2026-05-18T10:00:00Z").getTime();
      // 模拟合成 thinking 块在 stream 开启后 ~200ms 才出现 (Claude Code 一次性整段发出来)
      vi.setSystemTime(T0 + 200);
      const { rerender } = render(
        <ThinkingBlock text="abc" streaming startedAt={T0} />,
      );
      // 9 秒后文字开始流式,合成块转 done
      vi.setSystemTime(T0 + 9300);
      rerender(<ThinkingBlock text="abc" streaming={false} startedAt={T0} />);
      expect(screen.getByText("· 9s · 3 chars")).toBeInTheDocument();
    });
  });
});
