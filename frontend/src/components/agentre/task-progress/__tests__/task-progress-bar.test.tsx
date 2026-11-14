import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { TaskProgressBar } from "../task-progress-bar";

import type { TaskProgress } from "../types";
import { edgeClasses } from "@/__tests__/helpers/edge-classes";

const empty: TaskProgress = { tasks: [] };

const claude3: TaskProgress = {
  tasks: [
    { id: "t_1", description: "A", status: "completed" },
    { id: "t_2", description: "B", status: "running" },
    { id: "t_3", description: "C", status: "queued" },
  ],
};

describe("TaskProgressBar", () => {
  it("renders nothing when there are no tasks", () => {
    const { container } = render(<TaskProgressBar progress={empty} />);
    expect(container.firstChild).toBeNull();
  });

  it("shows ratio and current running task in collapsed mode", () => {
    render(<TaskProgressBar progress={claude3} />);
    expect(screen.getByText("1")).toBeInTheDocument();
    expect(screen.getByText("/")).toBeInTheDocument();
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getByText(/B/)).toBeInTheDocument();
  });

  it("toggles expanded state on header click and lists every task", () => {
    render(<TaskProgressBar progress={claude3} />);
    const header = screen.getByRole("button", { expanded: false });
    fireEvent.click(header);
    expect(screen.getByRole("button", { expanded: true })).toBeInTheDocument();
    expect(screen.getByText("A")).toBeInTheDocument();
    expect(screen.getByText("B")).toBeInTheDocument();
    expect(screen.getByText("C")).toBeInTheDocument();
  });

  it("Given an expanded list, When rendered, Then no row ends with a status word", () => {
    render(<TaskProgressBar progress={claude3} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));

    for (const word of ["DONE", "RUNNING", "QUEUED", "FAILED", "CANCELLED"]) {
      expect(screen.queryByText(word)).not.toBeInTheDocument();
    }
  });

  it("hides the 'current task' row when expanded", () => {
    render(<TaskProgressBar progress={claude3} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(screen.queryByText("当前")).not.toBeInTheDocument();
  });

  it.each([
    ["completed", "done", "text-status-running"],
    ["running", "running", "text-status-running"],
    ["failed", "failed", "text-status-error"],
    ["cancelled", "cancelled", "text-muted-foreground"],
    ["queued", "queued", "text-muted-foreground"],
  ] as const)(
    "Given an expanded %s row, Then its leading StatusMark is the %s mark in its fill color",
    (status, state, tone) => {
      render(
        <TaskProgressBar
          progress={{ tasks: [{ id: "t_1", description: "A", status }] }}
        />,
      );
      fireEvent.click(screen.getByRole("button", { expanded: false }));

      const mark = screen
        .getByText("A")
        .closest("li")!
        .querySelector('[data-slot="status-mark"]');

      expect(mark).toHaveAttribute("data-state", state);
      expect(mark).toHaveClass(tone);
      expect(mark).toHaveAttribute("role", "img");
    },
  );

  it("Given failed and cancelled rows, When expanded, Then they carry distinct icons and only the cancelled description is struck through", () => {
    render(
      <TaskProgressBar
        progress={{
          tasks: [
            { id: "t_1", description: "F", status: "failed" },
            { id: "t_2", description: "X", status: "cancelled" },
            { id: "t_3", description: "Q", status: "queued" },
          ],
        }}
      />,
    );
    fireEvent.click(screen.getByRole("button", { expanded: false }));

    const icon = (d: string) =>
      screen
        .getByText(d)
        .closest("li")!
        .querySelector("svg")!
        .getAttribute("class");
    expect(new Set([icon("F"), icon("X"), icon("Q")]).size).toBe(3);
    expect(screen.getByText("X")).toHaveClass("line-through");
    expect(screen.getByText("F")).not.toHaveClass("line-through");
    expect(screen.getByText("Q")).not.toHaveClass("line-through");
  });

  it("constrains the expanded list height and enables vertical scrolling", () => {
    const many: TaskProgress = {
      tasks: Array.from({ length: 30 }, (_, i) => ({
        id: `t_${i + 1}`,
        description: `task ${i + 1}`,
        status: "queued" as const,
      })),
    };
    render(<TaskProgressBar progress={many} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    const list = screen.getByRole("list");
    expect(list.className).toMatch(/overflow-y-auto/);
    expect(list.className).toMatch(/max-h-/);
  });

  it("auto-collapses to recap label 2s after all tasks complete", () => {
    vi.useFakeTimers();
    const allDone: TaskProgress = {
      tasks: [
        { id: "t_1", description: "A", status: "completed" },
        { id: "t_2", description: "B", status: "completed" },
      ],
    };
    try {
      const { rerender } = render(<TaskProgressBar progress={claude3} />);
      fireEvent.click(screen.getByRole("button", { expanded: false }));
      expect(
        screen.getByRole("button", { expanded: true }),
      ).toBeInTheDocument();
      rerender(<TaskProgressBar progress={allDone} />);
      act(() => {
        vi.advanceTimersByTime(2000);
      });
      expect(
        screen.getByRole("button", { expanded: false }),
      ).toBeInTheDocument();
      expect(screen.getByText(/All done/)).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });
});

describe("TaskProgressBar · 无边框", () => {
  it("Given an expanded list, When rendered, Then neither the region nor its rows draw dividing lines", () => {
    render(<TaskProgressBar progress={claude3} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));

    expect(edgeClasses(screen.getByRole("region"))).toEqual([]);
  });
});

describe("TaskProgressBar · 折叠态一行", () => {
  it("Given a running task, When collapsed, Then one header row carries the count, the current task and a thin bar, and no second row", () => {
    render(<TaskProgressBar progress={claude3} />);
    const header = screen.getByRole("button", { expanded: false });

    expect(screen.queryByRole("list")).not.toBeInTheDocument();
    expect(header).toHaveTextContent(/^1\/3/);
    expect(within(header).getByText("B")).toBeInTheDocument();
    const bar = within(header).getByRole("progressbar");
    expect(bar).toHaveAttribute("aria-valuenow", "1");
    expect(bar).toHaveClass("h-1");
  });

  it("Given a collapsed row, When rendered, Then it repeats neither the title, the completed label nor a percentage", () => {
    render(<TaskProgressBar progress={claude3} />);
    const header = screen.getByRole("button", { expanded: false });

    expect(header.textContent).not.toMatch(/Task Progress|Completed|%/);
  });

  it("Given a running task, When collapsed, Then its spinner uses the status-running token, not status-waiting", () => {
    render(<TaskProgressBar progress={claude3} />);
    const header = screen.getByRole("button", { expanded: false });
    const spinner = header.querySelector("svg.animate-spin");

    expect(spinner).toHaveClass("text-status-running");
    expect(spinner).not.toHaveClass("text-status-waiting");
  });

  // 规格 C6「运行中 spinner 用 status-running」：展开态列表的结构不变，但它那枚
  // 运行中 spinner 与状态词同样不能再借「需要你注意」的琥珀。
  it("Given a running task, When expanded, Then its row spinner uses status-running, not status-waiting", () => {
    render(<TaskProgressBar progress={claude3} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    const row = screen.getByText("B").closest("li")!;
    const spinner = row.querySelector('[data-state="running"]');

    expect(spinner).toHaveClass("text-status-running");
    expect(spinner).not.toHaveClass("text-status-waiting");
  });

  it("Given every task is completed, When collapsed, Then the single row shows the recap in place of the current task", () => {
    render(
      <TaskProgressBar
        progress={{
          tasks: [{ id: "t_1", description: "A", status: "completed" }],
        }}
      />,
    );
    const header = screen.getByRole("button", { expanded: false });

    expect(within(header).getByText(/All done/)).toBeInTheDocument();
  });
});
