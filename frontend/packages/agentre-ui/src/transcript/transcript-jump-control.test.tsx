import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { TranscriptJumpControl } from "./transcript-jump-control";

describe("TranscriptJumpControl", () => {
  it("Given 只是滚上去了、底下没有新东西, When 渲染, Then 仍然是那枚带文字的药丸，说「回到底部」", () => {
    render(<TranscriptJumpControl onJump={vi.fn()} />);

    const control = screen.getByTestId("transcript-jump-control");
    expect(control).toHaveTextContent("Back to bottom");
    // 圆形图标钮那一档已经取消：控件只有药丸一种形状。
    expect(control.className).not.toMatch(/rounded-full\b.*size-/);
  });

  it("Given 补齐带来了 N 条新内容, When 渲染, Then 药丸写出条数", () => {
    render(
      <TranscriptJumpControl
        onJump={vi.fn()}
        catchUp={{ newRows: 12, pendingDecisions: 0 }}
      />,
    );

    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "12 new",
    );
    expect(screen.queryByTestId("transcript-jump-pending")).toBeNull();
  });

  it("Given 补齐确实有新内容但报不出条数, When 渲染, Then 只说有新内容，不编一个数字", () => {
    render(
      <TranscriptJumpControl
        onJump={vi.fn()}
        catchUp={{ newRows: 0, pendingDecisions: 0 }}
      />,
    );

    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "New content",
    );
  });

  it("Given 补齐里还有没回答的待决策, When 渲染, Then 待处理项数是**文字**，不是只有一枚色点", () => {
    render(
      <TranscriptJumpControl
        onJump={vi.fn()}
        catchUp={{ newRows: 12, pendingDecisions: 3 }}
      />,
    );

    const pending = screen.getByTestId("transcript-jump-pending");
    expect(pending).toHaveTextContent("3 pending");
    // 可访问名把条数与待处理项数一并读出来 —— 不另挂 aria-label 盖掉它们。
    expect(screen.getByTestId("transcript-jump-control")).toHaveAccessibleName(
      /12 new.*3 pending/,
    );
  });

  it("Given 药丸, When 点它, Then 调用 onJump", async () => {
    const onJump = vi.fn();
    render(<TranscriptJumpControl onJump={onJump} />);

    await userEvent.click(screen.getByTestId("transcript-jump-control"));

    expect(onJump).toHaveBeenCalledOnce();
  });

  it("Given 视口下方还积着几轮, When 渲染, Then 药丸说出轮数", () => {
    render(<TranscriptJumpControl onJump={vi.fn()} turnsBelow={3} />);

    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "3 turns below",
    );
  });

  // 只上滚了一点、还在同一轮里:报「0 轮」是噪音。
  it("Given 下方不足一整轮, When 渲染, Then 退回「回到底部」", () => {
    render(<TranscriptJumpControl onJump={vi.fn()} turnsBelow={0} />);

    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "Back to bottom",
    );
  });

  // 两个数回答的是不同问题（「你离开时流进来多少」vs「你此刻落后多少」）。
  // 断连刚回来时前者才是用户要的，一行字的宽度也塞不下两个数。
  it("Given 补齐账与轮数同时在场, When 渲染, Then 补齐文案优先，轮数不出现", () => {
    render(
      <TranscriptJumpControl
        onJump={vi.fn()}
        catchUp={{ newRows: 12, pendingDecisions: 1 }}
        turnsBelow={3}
      />,
    );

    const control = screen.getByTestId("transcript-jump-control");
    expect(control).toHaveTextContent("12 new");
    expect(control).not.toHaveTextContent("turns below");
  });

  it("Given 调用方不传轮数（agentre-server 那端）, When 渲染, Then 呈现与今天一致", () => {
    render(<TranscriptJumpControl onJump={vi.fn()} />);

    expect(screen.getByTestId("transcript-jump-control")).toHaveTextContent(
      "Back to bottom",
    );
  });

  it("Given 药丸, When 渲染, Then 它浮在滚动带底部并在所在列里居中", () => {
    render(<TranscriptJumpControl onJump={vi.fn()} />);

    const control = screen.getByTestId("transcript-jump-control");
    const column = control.parentElement;
    expect(column?.className).toMatch(/(^|\s)sticky(\s|$)/);
    expect(column?.className).toMatch(/(^|\s)justify-center(\s|$)/);
    // ml-auto 那一档已取消：它会把药丸甩到整面板宽的滚动容器右缘。
    expect(control.className).not.toMatch(/(^|\s)ml-auto(\s|$)/);
  });

  // 两端的转录列并不同源：桌面端是 ml-10 + max-w-measure（靠左），
  // agentre-server 是 mx-auto + max-w-measure（本就居中，什么都不用给）。
  // 列几何是宿主布局，包不能写死其中一端的，否则另一端必然偏。
  it("Given 宿主自己的列几何, When 传进来, Then 落在浮层外壳上", () => {
    render(
      <TranscriptJumpControl
        onJump={vi.fn()}
        className="ml-10 max-w-measure"
      />,
    );

    const column = screen.getByTestId("transcript-jump-control").parentElement;
    expect(column?.className).toMatch(/(^|\s)ml-10(\s|$)/);
    expect(column?.className).toMatch(/(^|\s)max-w-measure(\s|$)/);
    expect(column?.className).toMatch(/(^|\s)justify-center(\s|$)/);
  });

  it("Given 宿主不给列几何（agentre-server 那端）, When 渲染, Then 外壳不带任何一端的列常量", () => {
    render(<TranscriptJumpControl onJump={vi.fn()} />);

    const column = screen.getByTestId("transcript-jump-control").parentElement;
    expect(column?.className).not.toMatch(/(^|\s)ml-10(\s|$)/);
    expect(column?.className).not.toMatch(/(^|\s)max-w-measure(\s|$)/);
  });

  // 外壳横跨整条正文列，若它接指针事件，就会在药丸所在的那条带上把底下整行内容的
  // 点击与选中一并挡掉 —— 旧的 ml-auto + w-fit 只挡右边那一小块，看不出来。
  it("Given 居中外壳横跨正文列, When 渲染, Then 只有药丸本身接指针事件", () => {
    render(<TranscriptJumpControl onJump={vi.fn()} />);

    const control = screen.getByTestId("transcript-jump-control");
    expect(control.parentElement?.className).toMatch(
      /(^|\s)pointer-events-none(\s|$)/,
    );
    expect(control.className).toMatch(/(^|\s)pointer-events-auto(\s|$)/);
  });
});
