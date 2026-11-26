// 手机 PWA 整修（D1）：宿主用自己的历史层让系统返回键关浮层，所以包内四块浮层都要
// 能「说出开合」并「被外部关掉」。全部可选；不传时与桌面一致。
import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeAll, describe, expect, it, vi } from "vitest";

import { BoardCard } from "../board-card";
import { BoardFilterBar } from "../board-filter-bar";
import { IssueBoard } from "../issue-board";
import { ProjectScopePicker } from "../project-scope-picker";
import { EMPTY_BOARD_QUERY } from "../query-types";
import { TaskFormShell } from "../task-form";
import { initialTaskFormValue } from "../use-task-form";
import type { BoardViewModel } from "../types";

beforeAll(() => {
  Element.prototype.scrollIntoView ??= vi.fn();
});

const ports = { onMove: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn() };
const card = { id: 7, stage: "todo" as const, title: "写文档" };

describe("D1 看板筛选弹层", () => {
  const bar = (over: Partial<React.ComponentProps<typeof BoardFilterBar>>) => (
    <BoardFilterBar
      query={EMPTY_BOARD_QUERY}
      labels={[]}
      ports={{ onQueryChange: vi.fn() }}
      sheet
      {...over}
    />
  );

  it("onOpenChange 在打开与关闭时各响一次", async () => {
    const onOpenChange = vi.fn();
    const user = userEvent.setup();
    render(bar({ onOpenChange }));
    await user.click(screen.getByTestId("filter-trigger"));
    expect(onOpenChange).toHaveBeenLastCalledWith(true);
    await user.click(screen.getByTestId("filter-sheet-apply"));
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
    expect(onOpenChange).toHaveBeenCalledTimes(2);
  });

  it("open={false} 由宿主关掉已开的弹层；open={true} 直接打开", () => {
    const { rerender } = render(bar({ open: true }));
    expect(screen.getByTestId("filter-sheet")).toBeInTheDocument();
    rerender(bar({ open: false }));
    expect(screen.queryByTestId("filter-sheet")).toBeNull();
  });

  it("不传：照旧自管开合", async () => {
    const user = userEvent.setup();
    render(bar({}));
    await user.click(screen.getByTestId("filter-trigger"));
    expect(screen.getByTestId("filter-sheet")).toBeInTheDocument();
  });

  it("桌面 Popover 同样回调", async () => {
    const onOpenChange = vi.fn();
    const user = userEvent.setup();
    render(bar({ sheet: false, onOpenChange }));
    await user.click(screen.getByTestId("filter-trigger"));
    expect(onOpenChange).toHaveBeenLastCalledWith(true);
  });
});

describe("D1 项目范围弹层", () => {
  const picker = (
    over: Partial<React.ComponentProps<typeof ProjectScopePicker>>,
  ) => (
    <ProjectScopePicker
      scope={{ kind: "all" }}
      projects={[{ id: 1, name: "alpha", depth: 0, unfinished: 1 }]}
      onScopeChange={vi.fn()}
      sheet
      {...over}
    />
  );

  it("onOpenChange 随开合触发", async () => {
    const onOpenChange = vi.fn();
    const user = userEvent.setup();
    render(picker({ onOpenChange }));
    await user.click(screen.getByRole("button", { expanded: false }));
    expect(onOpenChange).toHaveBeenLastCalledWith(true);
    await user.click(screen.getByTestId("scope-sheet-apply"));
    expect(onOpenChange).toHaveBeenLastCalledWith(false);
  });

  it("open 受控：宿主翻成 false 弹层就关", () => {
    const { rerender } = render(picker({ open: true }));
    expect(screen.getByTestId("scope-sheet")).toBeInTheDocument();
    rerender(picker({ open: false }));
    expect(screen.queryByTestId("scope-sheet")).toBeNull();
  });

  it("不传：照旧自管开合", async () => {
    const user = userEvent.setup();
    render(picker({}));
    await user.click(screen.getByRole("button", { expanded: false }));
    expect(screen.getByTestId("scope-sheet")).toBeInTheDocument();
  });
});

describe("D1 卡片菜单", () => {
  it("触屏档：菜单开合回调，menuOpen={false} 关掉已开的菜单", async () => {
    const onMenuOpenChange = vi.fn();
    const user = userEvent.setup();
    const props = { card, ports, touch: true, onMenuOpenChange };
    const { rerender } = render(<BoardCard {...props} />);
    await user.click(screen.getByTestId("board-card-menu-7"));
    expect(onMenuOpenChange).toHaveBeenLastCalledWith(true);
    expect(screen.getByTestId("board-card-sheet-7")).toBeInTheDocument();
    rerender(<BoardCard {...props} menuOpen={false} />);
    expect(screen.queryByTestId("board-card-sheet-7")).toBeNull();
  });

  it("长按打开同样回调", () => {
    vi.useFakeTimers();
    const onMenuOpenChange = vi.fn();
    render(
      <BoardCard
        card={card}
        ports={ports}
        touch
        onMenuOpenChange={onMenuOpenChange}
      />,
    );
    fireEvent.pointerDown(screen.getByTestId("board-card-body-7"), {
      pointerType: "touch",
    });
    act(() => {
      vi.advanceTimersByTime(600);
    });
    vi.useRealTimers();
    expect(onMenuOpenChange).toHaveBeenCalledWith(true);
  });

  it("不传：照旧", async () => {
    const user = userEvent.setup();
    render(<BoardCard card={card} ports={ports} touch />);
    await user.click(screen.getByTestId("board-card-menu-7"));
    expect(screen.getByTestId("board-card-sheet-7")).toBeInTheDocument();
  });

  it("IssueBoard 透传：openCardMenuId 指到哪张卡就开哪张，回调带卡号", async () => {
    const model: BoardViewModel = {
      filtering: false,
      columns: {
        todo: { cards: [card, { ...card, id: 8 }], total: 2 },
        doing: { cards: [], total: 0 },
        review: { cards: [], total: 0 },
        done: { cards: [], total: 0 },
      },
    };
    const onCardMenuOpenChange = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      <IssueBoard
        viewModel={model}
        ports={ports}
        layout="stage"
        openCardMenuId={null}
        onCardMenuOpenChange={onCardMenuOpenChange}
      />,
    );
    await user.click(screen.getByTestId("board-card-menu-8"));
    expect(onCardMenuOpenChange).toHaveBeenCalledWith(8, true);
    rerender(
      <IssueBoard
        viewModel={model}
        ports={ports}
        layout="stage"
        openCardMenuId={8}
        onCardMenuOpenChange={onCardMenuOpenChange}
      />,
    );
    expect(screen.getByTestId("board-card-sheet-8")).toBeInTheDocument();
    expect(screen.queryByTestId("board-card-sheet-7")).toBeNull();
  });
});

describe("D1 任务 ⋯ 菜单", () => {
  const form = (
    over: Partial<React.ComponentProps<typeof TaskFormShell>> = {},
  ) => (
    <TaskFormShell
      initial={initialTaskFormValue({ issue: { id: 3, title: "t" } })}
      projects={[]}
      labels={[]}
      agentOptions={[]}
      onSave={vi.fn()}
      onDelete={vi.fn()}
      {...over}
    />
  );

  it("onMoreMenuOpenChange 随开合触发，moreMenuOpen={false} 由宿主关掉", async () => {
    const onMoreMenuOpenChange = vi.fn();
    const user = userEvent.setup();
    const { rerender } = render(
      form({ onMoreMenuOpenChange, moreMenuOpen: false }),
    );
    // 受控为 false 时点击只会上报意图
    await user.click(screen.getByTestId("task-form-more"));
    expect(onMoreMenuOpenChange).toHaveBeenLastCalledWith(true);
    rerender(form({ onMoreMenuOpenChange, moreMenuOpen: true }));
    expect(screen.getByTestId("task-form-delete")).toBeInTheDocument();
    rerender(form({ onMoreMenuOpenChange, moreMenuOpen: false }));
    expect(screen.queryByTestId("task-form-delete")).toBeNull();
  });

  it("不传：照旧自管", async () => {
    const user = userEvent.setup();
    render(form());
    await user.click(screen.getByTestId("task-form-more"));
    expect(screen.getByTestId("task-form-delete")).toBeInTheDocument();
  });
});
