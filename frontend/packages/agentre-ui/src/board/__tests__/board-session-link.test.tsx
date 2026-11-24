import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BoardCardMenu } from "../card-menu";
import { IssueBoard } from "../issue-board";
import type {
  BoardCardView,
  BoardPorts,
  BoardStage,
  BoardViewModel,
} from "../types";

const NOW = Date.UTC(2026, 7, 27, 12, 0, 0);

function card(id: number, over: Partial<BoardCardView> = {}): BoardCardView {
  return { id, stage: "todo", title: `任务 ${id}`, ...over };
}

function column(cards: BoardCardView[]) {
  return { cards, total: cards.length };
}

function viewModel(
  columns: Partial<Record<BoardStage, ReturnType<typeof column>>>,
): BoardViewModel {
  return { filtering: false, columns };
}

function ports(over: Partial<BoardPorts> = {}): BoardPorts {
  return { onMove: vi.fn(), onEdit: vi.fn(), onDelete: vi.fn(), ...over };
}

function renderBoard(model: BoardViewModel, p: BoardPorts = ports()) {
  return render(<IssueBoard viewModel={model} ports={p} nowMs={NOW} />);
}

describe("开始会话与关联会话（宿主可选）", () => {
  const linked = [
    { id: 31, title: "修 OAuth", agentName: "Reviewer", status: "running" },
    { id: 32, title: "补测试", agentName: "Coder", status: "error" },
  ] as const;

  it("Given the host provides onStartSession, When the menu opens, Then Start session is the first item and fires with the card id", async () => {
    const user = userEvent.setup();
    const p = ports({ onStartSession: vi.fn() });
    renderBoard(viewModel({ todo: column([card(1)]) }), p);

    await user.click(screen.getByTestId("board-card-menu-1"));
    const items = screen.getAllByRole("menuitem");
    expect(items[0]).toHaveTextContent("Start session");
    await user.click(items[0]);
    expect(p.onStartSession).toHaveBeenCalledWith(1);
  });

  it("Given no onStartSession port, When the menu opens, Then there is no Start session item and Edit stays first", async () => {
    const user = userEvent.setup();
    renderBoard(viewModel({ todo: column([card(1)]) }));

    await user.click(screen.getByTestId("board-card-menu-1"));
    expect(screen.queryByText("Start session")).not.toBeInTheDocument();
    expect(screen.getAllByRole("menuitem")[0]).toHaveTextContent("Edit");
  });

  it("Given the touch sheet and the port, When it opens, Then Start session is offered", async () => {
    const user = userEvent.setup();
    const p = ports({ onStartSession: vi.fn() });
    render(<BoardCardMenu cardId={6} stage="todo" ports={p} touch />);

    await user.click(screen.getByTestId("board-card-menu-6"));
    await user.click(screen.getByRole("button", { name: "Start session" }));
    expect(p.onStartSession).toHaveBeenCalledWith(6);
  });

  it("Given a card with linked sessions, When rendered, Then each row shows status, title and agent and opens its session", async () => {
    const user = userEvent.setup();
    const p = ports({ onOpenSession: vi.fn() });
    renderBoard(
      viewModel({ todo: column([card(1, { sessions: [...linked] })]) }),
      p,
    );

    const block = screen.getByTestId("board-card-sessions-1");
    const rows = within(block).getAllByRole("button");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("修 OAuth");
    expect(rows[0]).toHaveTextContent("Reviewer");
    expect(within(rows[0]).getByLabelText("Running")).toBeInTheDocument();
    expect(within(rows[1]).getByLabelText("Error")).toBeInTheDocument();

    await user.click(rows[1]);
    expect(p.onOpenSession).toHaveBeenCalledWith(32);
    // 点会话行不当成「点开编辑」。
    expect(p.onEdit).not.toHaveBeenCalled();
  });

  it("Given no sessions or an empty list, When rendered, Then the block is absent", () => {
    renderBoard(
      viewModel({ todo: column([card(1), card(3, { sessions: [] })]) }),
    );
    expect(screen.queryByTestId("board-card-sessions-1")).toBeNull();
    expect(screen.queryByTestId("board-card-sessions-3")).toBeNull();
  });

  it("Given neither prop, When rendered, Then the card keeps only its body and menu buttons", () => {
    renderBoard(viewModel({ todo: column([card(1)]) }));
    expect(
      screen.getByTestId("board-card-1").querySelectorAll("button"),
    ).toHaveLength(2);
  });
});

describe("按卡片聚焦（?issue=）", () => {
  afterEach(() => {
    delete (Element.prototype as { scrollIntoView?: unknown }).scrollIntoView;
  });

  it("Given focusCardId, When the card is on the board, Then it scrolls into view and is highlighted", () => {
    const scroll = vi.fn();
    Element.prototype.scrollIntoView = scroll;
    render(
      <IssueBoard
        viewModel={viewModel({
          todo: column([card(1)]),
          doing: column([card(2, { stage: "doing" })]),
        })}
        ports={ports()}
        nowMs={NOW}
        focusCardId={2}
      />,
    );
    expect(screen.getByTestId("board-card-2")).toHaveAttribute(
      "data-focused",
      "true",
    );
    expect(screen.getByTestId("board-card-1")).not.toHaveAttribute(
      "data-focused",
    );
    expect(scroll).toHaveBeenCalledTimes(1);
  });

  it("Given a focused card folded into the done row, When rendered, Then the done column expands to reveal it", () => {
    Element.prototype.scrollIntoView = vi.fn();
    const doneCards = Array.from({ length: 8 }, (_, index) =>
      card(100 + index, { stage: "done", updatedAt: NOW - index * 1000 }),
    );
    render(
      <IssueBoard
        viewModel={viewModel({ done: column(doneCards) })}
        ports={ports()}
        nowMs={NOW}
        focusCardId={107}
      />,
    );
    expect(screen.getByTestId("board-card-107")).toHaveAttribute(
      "data-focused",
      "true",
    );
  });

  it("Given no focusCardId, When rendered, Then nothing is highlighted", () => {
    renderBoard(viewModel({ todo: column([card(1)]) }));
    expect(document.querySelector("[data-focused='true']")).toBeNull();
  });
});
