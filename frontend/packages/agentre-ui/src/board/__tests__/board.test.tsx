import { fireEvent, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import { IssueBoard } from "../issue-board";
import type {
  BoardCardView,
  BoardPorts,
  BoardStage,
  BoardViewModel,
} from "../types";

const NOW = Date.UTC(2026, 7, 27, 12, 0, 0);

function card(id: number, over: Partial<BoardCardView> = {}): BoardCardView {
  return {
    id,
    stage: "todo",
    title: `任务 ${id}`,
    ...over,
  };
}

function column(cards: BoardCardView[], total = cards.length) {
  return { cards, total };
}

function viewModel(over: Partial<BoardViewModel> = {}): BoardViewModel {
  return {
    filtering: false,
    columns: {
      todo: column([card(1)]),
      doing: column([card(2, { stage: "doing" })]),
      review: column([]),
      done: column([]),
    },
    ...over,
  };
}

function ports(over: Partial<BoardPorts> = {}): BoardPorts {
  return {
    onMove: vi.fn(),
    onEdit: vi.fn(),
    onDelete: vi.fn(),
    ...over,
  };
}

function renderBoard(
  model: BoardViewModel,
  p: BoardPorts = ports(),
  drag = {},
) {
  return render(
    <IssueBoard viewModel={model} ports={p} nowMs={NOW} drag={drag} />,
  );
}

describe("看板外壳：四列固定、各列自滚、列头常驻", () => {
  it("Given any view model, When the board renders, Then exactly the four stages appear in fixed order", () => {
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }));

    const stages = Array.from(
      document.querySelectorAll('[data-slot="board-column"]'),
    ).map((node) => node.getAttribute("data-stage"));

    expect(stages).toEqual<BoardStage[]>(["todo", "doing", "review", "done"]);
  });

  it("Given a column, When it is inspected, Then its header sits outside the column's own vertical scroller and the board scrolls horizontally", () => {
    renderBoard(viewModel());

    const todo = screen.getByTestId("board-column-todo");
    const scroller = within(todo).getByTestId("board-column-scroll-todo");
    const header = within(todo).getByTestId("board-column-header-todo");

    // 列头常驻 = 它根本不在滚动容器里，滚卡片不会把它带走。
    expect(scroller.contains(header)).toBe(false);
    expect(scroller.className).toContain("overflow-y-auto");
    expect(screen.getByTestId("issue-board").className).toContain(
      "overflow-x-auto",
    );
  });
});

describe("列头计数", () => {
  it("Given no filter, When the header renders, Then it shows the column total", () => {
    renderBoard(viewModel({ columns: { todo: column([card(1)], 9) } }));

    expect(
      within(screen.getByTestId("board-column-header-todo")).getByTestId(
        "board-column-count-todo",
      ),
    ).toHaveTextContent(/^9$/);
  });

  it("Given a filter is active, When the header renders, Then it shows 命中 / 全部 and a zero-hit column stays in place", () => {
    renderBoard(
      viewModel({
        filtering: true,
        columns: {
          todo: { cards: [card(1)], total: 9, matched: 1 },
          doing: { cards: [], total: 4, matched: 0 },
        },
      }),
    );

    expect(screen.getByTestId("board-column-count-todo")).toHaveTextContent(
      "1 / 9",
    );
    expect(screen.getByTestId("board-column-count-doing")).toHaveTextContent(
      "0 / 4",
    );
    expect(screen.getByTestId("board-column-doing")).toBeInTheDocument();
  });
});

describe("卡片", () => {
  it("Given a card, When it renders, Then number, title, two labels and an outlined +N overflow are shown", () => {
    renderBoard(
      viewModel({
        columns: {
          todo: column([
            card(7, {
              title: "修好拖拽",
              labels: [
                { id: 1, name: "缺陷", tone: "red" },
                { id: 2, name: "前端", tone: "blue" },
                { id: 3, name: "紧急", tone: "red_solid" },
              ],
            }),
          ]),
        },
      }),
    );

    const node = screen.getByTestId("board-card-7");
    expect(within(node).getByText("#7")).toBeInTheDocument();
    expect(within(node).getByText("修好拖拽")).toBeInTheDocument();
    expect(within(node).getByText("缺陷")).toBeInTheDocument();
    expect(within(node).getByText("前端")).toBeInTheDocument();
    expect(within(node).queryByText("紧急")).not.toBeInTheDocument();

    // 溢出计数是**描边**，暗色下 --secondary 与 --popover 同色会整个消失。
    const overflow = within(node).getByText("+1");
    expect(overflow.className).toContain("border-border-strong");
    expect(overflow.className).toContain("text-muted-foreground");
    expect(overflow.className).not.toContain("bg-secondary");
  });

  it("Given a card with a description, When the meta line renders, Then it carries the description mark and the relative time", () => {
    renderBoard(
      viewModel({
        columns: {
          todo: column([
            card(7, { hasDescription: true, updatedAt: NOW - 3 * 3_600_000 }),
          ]),
        },
      }),
    );

    const node = screen.getByTestId("board-card-7");
    expect(
      within(node).getByTestId("board-card-has-description"),
    ).toBeVisible();
    expect(within(node).getByTestId("board-card-time")).toHaveTextContent(
      "3h ago",
    );
  });

  it("Given a card without a description, When the meta line renders, Then no description mark is drawn", () => {
    renderBoard(viewModel({ columns: { todo: column([card(7)]) } }));

    expect(
      within(screen.getByTestId("board-card-7")).queryByTestId(
        "board-card-has-description",
      ),
    ).not.toBeInTheDocument();
  });
});

describe("卡片菜单", () => {
  it("Given a card menu, When the card is only hovered or focused, Then the trigger is revealed by both", () => {
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }));

    const trigger = screen.getByTestId("board-card-menu-1");
    expect(trigger.className).toContain("opacity-0");
    expect(trigger.className).toContain("group-hover/card:opacity-100");
    // 「hover 才出现」不能把键盘用户挡在外面。
    expect(trigger.className).toContain("group-focus-within/card:opacity-100");
  });

  it("Given keyboard navigation, When the card is tabbed past, Then the menu trigger takes focus and Enter opens the menu", async () => {
    const user = userEvent.setup();
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }));

    await user.tab();
    await user.tab();
    expect(screen.getByTestId("board-card-menu-1")).toHaveFocus();

    await user.keyboard("{Enter}");
    expect(screen.getByRole("menuitem", { name: "Edit" })).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "Move to" }),
    ).toBeInTheDocument();
    expect(
      screen.getByRole("menuitem", { name: "Delete" }),
    ).toBeInTheDocument();
  });

  it("Given an open card menu, When 编辑 / 删除 are chosen, Then the matching port fires with the card id", async () => {
    const user = userEvent.setup();
    const p = ports();
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }), p);

    await user.click(screen.getByTestId("board-card-menu-1"));
    await user.click(screen.getByRole("menuitem", { name: "Edit" }));
    expect(p.onEdit).toHaveBeenCalledWith(1);

    await user.click(screen.getByTestId("board-card-menu-1"));
    await user.click(screen.getByRole("menuitem", { name: "Delete" }));
    expect(p.onDelete).toHaveBeenCalledWith(1);
  });

  it("Given an open card menu, When a stage is chosen under 移动到, Then onMove carries that stage", async () => {
    const user = userEvent.setup();
    const p = ports();
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }), p);

    await user.click(screen.getByTestId("board-card-menu-1"));
    await user.click(screen.getByRole("menuitem", { name: "Move to" }));
    // 子菜单项走 fireEvent：user-event 的指针序列在 happy-dom 里选不中 radix 的
    // SubContent 项（radix 自己的那份用例也没有一条点开子菜单），与本组件无关。
    fireEvent.click(screen.getByRole("menuitem", { name: "In progress" }));

    expect(p.onMove).toHaveBeenCalledWith(1, "doing");
  });

  it("Given the menu sits on a popover surface, When its divider renders, Then it uses --border-strong", async () => {
    const user = userEvent.setup();
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }));

    await user.click(screen.getByTestId("board-card-menu-1"));

    const separator = document.querySelector(
      '[data-slot="dropdown-menu-separator"]',
    );
    // --border 在 --popover 上暗色对比度 1.06：整条线消失。
    expect(separator?.className).toContain("bg-border-strong");
  });
});

describe("拖拽三态", () => {
  it("Given a card left behind in place, When it renders, Then it is a dashed ghost", () => {
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }), ports(), {
      card: () => ({ state: "ghost" as const }),
    });

    const body = screen.getByTestId("board-card-body-1");
    expect(body.className).toContain("border-dashed");
  });

  it("Given the card being dragged, When it renders, Then it is lifted and rotated", () => {
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }), ports(), {
      card: () => ({ state: "lifted" as const }),
    });

    const body = screen.getByTestId("board-card-body-1");
    expect(body.className).toContain("rotate-2");
    expect(body.className).toContain("shadow-overlay");
  });

  it("Given a column under the pointer, When it renders, Then the whole column is highlighted, not just the card border", () => {
    renderBoard(viewModel(), ports(), {
      column: (stage: BoardStage) =>
        stage === "doing" ? { dropState: "over" as const } : undefined,
    });

    expect(screen.getByTestId("board-column-doing")).toHaveAttribute(
      "data-drop-state",
      "over",
    );
    expect(screen.getByTestId("board-column-doing").className).toContain(
      "bg-primary-soft",
    );
    expect(screen.getByTestId("board-column-todo")).not.toHaveAttribute(
      "data-drop-state",
    );
  });
});

describe("空列", () => {
  it("Given an empty column, When it renders, Then it says what it can do rather than 暂无", () => {
    renderBoard(viewModel());

    expect(
      within(screen.getByTestId("board-column-review")).getByText("Drop here"),
    ).toBeInTheDocument();
  });
});

describe("命中片段", () => {
  const hit = viewModel({
    filtering: true,
    keyword: "缓存",
    columns: {
      todo: column([
        card(1, { title: "重写缓存层", description: "" }),
        card(2, {
          title: "收尾",
          description: `${"补".repeat(40)}缓存命中率上不去${"补".repeat(40)}`,
        }),
      ]),
    },
  });

  it("Given a keyword, When a card title matches, Then the matching run is marked out", () => {
    renderBoard(hit);

    const marks = within(screen.getByTestId("board-card-1")).getAllByText(
      "缓存",
    );
    expect(marks[0].tagName).toBe("MARK");
    // 命中在标题里 → 不再摘一行，那是同一件事说两遍。
    expect(screen.queryByTestId("board-card-excerpt-1")).toBeNull();
  });

  it("Given a keyword that only the description carries, When the card renders, Then one line of it is quoted, and only one", () => {
    renderBoard(hit);

    const excerpt = screen.getByTestId("board-card-excerpt-2");
    expect(excerpt).toHaveTextContent("缓存命中率上不去");
    // 一行封顶：卡片不是正文的容器。
    expect(excerpt.className).toContain("truncate");
    expect(within(excerpt).getByText("缓存").tagName).toBe("MARK");
  });

  it("Given no keyword, When cards render, Then nothing is marked and no card quotes its description", () => {
    renderBoard(viewModel());

    expect(document.querySelector("mark")).toBeNull();
    expect(screen.queryByTestId(/^board-card-excerpt-/)).toBeNull();
  });
});

describe("从一列建任务", () => {
  it("Given a column header, When its + is pressed, Then the new task starts in that very column", async () => {
    const user = userEvent.setup();
    const onCreateTask = vi.fn();
    renderBoard(viewModel(), ports({ onCreateTask }));

    await user.click(screen.getByTestId("board-column-add-doing"));

    // 表单的阶段由它预置（`initialTaskFormValue({ stage })`），不必建完再拖一次。
    expect(onCreateTask).toHaveBeenCalledWith("doing");
  });

  it("Given a host that offers no create port, When the board renders, Then no column grows a + it cannot honour", () => {
    renderBoard(viewModel(), ports());

    expect(screen.queryByTestId("board-column-add-todo")).toBeNull();
  });
});

describe("整列都是落点", () => {
  it("Given a column, When the drop listener is attached, Then it covers the whole column including the pinned header", () => {
    const setNodeRef = vi.fn();
    renderBoard(viewModel(), ports(), {
      column: () => ({ setNodeRef }),
    });

    // 落点挂在列本身：拖到列头那一条带上照样接得住，也照样整列高亮。
    const todo = screen.getByTestId("board-column-todo");
    expect(setNodeRef).toHaveBeenCalledWith(todo);
    expect(todo.contains(screen.getByTestId("board-column-header-todo"))).toBe(
      true,
    );
  });
});

describe("已完成列的折叠", () => {
  const doneCards = Array.from({ length: 8 }, (_, index) =>
    card(100 + index, { stage: "done" }),
  );

  it("Given no filter, When the done column has more than the visible limit, Then the rest collapse into one row that expands in place", async () => {
    const user = userEvent.setup();
    renderBoard(viewModel({ columns: { done: column(doneCards) } }));

    const done = screen.getByTestId("board-column-done");
    expect(within(done).getAllByTestId(/^board-card-\d+$/)).toHaveLength(5);

    await user.click(within(done).getByText("3 more done"));

    expect(within(done).getAllByTestId(/^board-card-\d+$/)).toHaveLength(8);
    expect(within(done).queryByText("3 more done")).not.toBeInTheDocument();
  });

  it("Given no filter, When the done column collapses, Then what stays visible is the most recently finished, not whichever the drag order put first", async () => {
    const user = userEvent.setup();
    // 列内次序是人拖出来的 position，最老的那几张排在最前面。
    const byPosition = Array.from({ length: 8 }, (_, index) =>
      card(200 + index, {
        stage: "done",
        updatedAt: NOW - (8 - index) * 86_400_000,
      }),
    );
    renderBoard(viewModel({ columns: { done: column(byPosition) } }));

    const done = screen.getByTestId("board-column-done");
    const shown = within(done)
      .getAllByTestId(/^board-card-\d+$/)
      .map((node) => node.getAttribute("data-testid"));

    // 「只渲染**最近**若干张」：刚完成的那五张，而不是列顶那五张。
    expect(shown).toEqual([
      "board-card-203",
      "board-card-204",
      "board-card-205",
      "board-card-206",
      "board-card-207",
    ]);

    // 折叠只是把老的藏起来，剩下这几张照旧按列内位置排。
    await user.click(within(done).getByText("3 more done"));
    expect(
      within(done)
        .getAllByTestId(/^board-card-\d+$/)
        .map((node) => node.getAttribute("data-testid")),
    ).toEqual(byPosition.map((c) => `board-card-${c.id}`));
  });

  it("Given a filter is active, When the done column renders, Then it is expanded so hits cannot hide inside the collapsed row", () => {
    renderBoard(
      viewModel({
        filtering: true,
        columns: { done: { cards: doneCards, total: 20, matched: 8 } },
      }),
    );

    const done = screen.getByTestId("board-column-done");
    expect(within(done).getAllByTestId(/^board-card-\d+$/)).toHaveLength(8);
    expect(within(done).queryByText(/more done/)).not.toBeInTheDocument();
  });
});

describe("骨架与空态", () => {
  it("Given the board is loading, When it renders, Then four columns of skeleton cards stand in, with no empty state", () => {
    renderBoard(viewModel({ loading: true, columns: {} }));

    expect(screen.getAllByTestId(/^board-skeleton-column-/)).toHaveLength(4);
    expect(screen.queryByTestId("board-empty-state")).not.toBeInTheDocument();
  });

  it("Given nothing to show and no filter, When the board renders, Then the way out is 新建任务", async () => {
    const user = userEvent.setup();
    const onCreateTask = vi.fn();
    renderBoard(viewModel({ columns: {} }), ports({ onCreateTask }));

    const empty = screen.getByTestId("board-empty-state");
    expect(
      within(empty).getByText("No tasks in this project yet"),
    ).toBeInTheDocument();

    await user.click(within(empty).getByRole("button", { name: "New task" }));
    expect(onCreateTask).toHaveBeenCalled();
  });

  it("Given nothing matches an active filter, When the board renders, Then the way out is 清除筛选", async () => {
    const user = userEvent.setup();
    const onClearFilters = vi.fn();
    renderBoard(
      viewModel({ filtering: true, columns: {} }),
      ports({ onClearFilters }),
    );

    const empty = screen.getByTestId("board-empty-state");
    expect(
      within(empty).getByText("No tasks match your filters"),
    ).toBeInTheDocument();

    await user.click(
      within(empty).getByRole("button", { name: "Clear filters" }),
    );
    expect(onClearFilters).toHaveBeenCalled();
  });
});

describe("焦点环", () => {
  it("Given an interactive card, When it renders, Then it uses the project-wide focus ring", () => {
    renderBoard(viewModel({ columns: { todo: column([card(1)]) } }));

    const body = screen.getByTestId("board-card-body-1");
    expect(body.className).toContain("focus-visible:ring-[3px]");
    expect(body.className).toContain("focus-visible:ring-ring/40");
  });
});
