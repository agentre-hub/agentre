// 手机 PWA 整修（J3、F20/I3）：看板卡片可不显示 #N、搜索占位可改、搜索框 44px 且计数带标签。
// 全部走可选入参 / 触屏档；不传时与桌面一致（每条后面都有一条「不传」的对照）。
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { TouchProvider } from "../../ui/touch";
import { BoardFilterBar } from "../board-filter-bar";
import { IssueBoard } from "../issue-board";
import { EMPTY_BOARD_QUERY } from "../query-types";
import type { BoardViewModel } from "../types";

const model: BoardViewModel = {
  filtering: false,
  columns: {
    todo: { cards: [{ id: 7, stage: "todo", title: "写文档" }], total: 1 },
    doing: { cards: [], total: 0 },
    review: { cards: [], total: 0 },
    done: { cards: [], total: 0 },
  },
};
const ports = { onMove: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn() };

function bar(over: Partial<React.ComponentProps<typeof BoardFilterBar>> = {}) {
  return render(
    <BoardFilterBar
      query={EMPTY_BOARD_QUERY}
      labels={[]}
      ports={{ onQueryChange: vi.fn() }}
      matchedCount={6}
      {...over}
    />,
  );
}

describe("J3 看板卡片编号与搜索占位", () => {
  it("不传：卡片显示 #7，占位是默认文案", () => {
    render(<IssueBoard viewModel={model} ports={ports} />);
    expect(screen.getByText("#7")).toBeInTheDocument();
    bar();
    expect(screen.getByRole("searchbox")).toHaveAttribute(
      "placeholder",
      "Search tasks or #number…",
    );
  });

  it("hideCardNumber：卡片不渲染 #7，标题仍在", () => {
    render(<IssueBoard viewModel={model} ports={ports} hideCardNumber />);
    expect(screen.queryByText("#7")).toBeNull();
    expect(screen.getByText("写文档")).toBeInTheDocument();
  });

  it("searchPlaceholder 覆盖占位与无障碍名", () => {
    bar({ searchPlaceholder: "搜索任务…" });
    const box = screen.getByRole("searchbox");
    expect(box).toHaveAttribute("placeholder", "搜索任务…");
    expect(box).toHaveAccessibleName("搜索任务…");
  });
});

describe("F20/I3 看板搜索框触屏档", () => {
  it("不传：32px 高、计数只有数字", () => {
    bar();
    expect(document.querySelector('[data-slot="search-input"]')).toHaveClass(
      "h-8",
    );
    expect(screen.getByTestId("board-search-count")).toHaveTextContent(/^6$/);
  });

  it("sheet 档：44px 高、计数带标签", () => {
    bar({ sheet: true });
    expect(document.querySelector('[data-slot="search-input"]')).toHaveClass(
      "h-11",
    );
    expect(screen.getByTestId("board-search-count")).toHaveTextContent(
      "6 tasks",
    );
  });

  it("TouchProvider 同样切到触屏档", () => {
    render(
      <TouchProvider>
        <BoardFilterBar
          query={EMPTY_BOARD_QUERY}
          labels={[]}
          ports={{ onQueryChange: vi.fn() }}
          matchedCount={1}
        />
      </TouchProvider>,
    );
    expect(document.querySelector('[data-slot="search-input"]')).toHaveClass(
      "h-11",
    );
    expect(screen.getByTestId("board-search-count")).toHaveTextContent(
      "1 task",
    );
  });
});
