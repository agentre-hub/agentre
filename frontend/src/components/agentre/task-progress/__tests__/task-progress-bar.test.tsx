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
    expect(screen.getByText("DONE")).toBeInTheDocument();
    expect(screen.getByText("RUNNING")).toBeInTheDocument();
    expect(screen.getByText("QUEUED")).toBeInTheDocument();
  });

  it("hides the 'current task' row when expanded", () => {
    render(<TaskProgressBar progress={claude3} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    expect(screen.queryByText("当前")).not.toBeInTheDocument();
  });

  it.each([
    ["DONE", "text-status-running", "text-status-running-text"],
    ["RUNNING", "text-status-running", "text-status-running-text"],
    ["FAILED", "text-status-error", "text-destructive-text"],
  ])(
    "Given an expanded %s row, Then its icon keeps the fill color and only the status word takes the -text role",
    (word, fill, text) => {
      // 一行里同一个状态出现两次：左边的图标是图形，右边的 DONE / RUNNING 是字。
      // 图形留在饱和的填充色上，要读的字才换 -text 角色（docs/design.md §3.5）。
      render(
        <TaskProgressBar
          progress={{
            tasks: [
              { id: "t_1", description: "A", status: "completed" },
              { id: "t_2", description: "B", status: "running" },
              { id: "t_3", description: "C", status: "failed" },
            ],
          }}
        />,
      );
      fireEvent.click(screen.getByRole("button", { expanded: false }));

      const label = screen.getByText(word);
      const icon = label.closest("li")?.querySelector("svg");

      expect(label).toHaveClass(text);
      expect(icon).toHaveClass(fill);
      expect(icon).not.toHaveClass(text);
    },
  );

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
  it("Given a running task, When expanded, Then its row spinner and status word use status-running, not status-waiting", () => {
    render(<TaskProgressBar progress={claude3} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));
    const row = screen.getByText("B").closest("li")!;
    const spinner = row.querySelector("svg.animate-spin");

    expect(spinner).toHaveClass("text-status-running");
    expect(spinner).not.toHaveClass("text-status-waiting");
    expect(within(row).getByText("RUNNING")).toHaveClass(
      "text-status-running-text",
    );
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
