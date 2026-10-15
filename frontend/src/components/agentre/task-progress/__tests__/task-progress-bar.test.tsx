import { act, fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

import { TaskProgressBar } from "../task-progress-bar";

import type { TaskProgress } from "../types";

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
    ["RUNNING", "text-status-waiting", "text-status-waiting-text"],
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

/** 画结构线的边宽类（含变体前缀）：`border`、`border-t`、`hover:border` 等。 */
function edged(root: Element): string[] {
  return [root, ...Array.from(root.querySelectorAll("*"))].flatMap((el) =>
    Array.from(el.classList).filter((c) =>
      /^(?:[\w-]+:)*border(?:-[tblrxy])?$/.test(c),
    ),
  );
}

describe("TaskProgressBar · 无边框", () => {
  it("Given an expanded list, When rendered, Then neither the region nor its rows draw dividing lines", () => {
    render(<TaskProgressBar progress={claude3} />);
    fireEvent.click(screen.getByRole("button", { expanded: false }));

    expect(edged(screen.getByRole("region"))).toEqual([]);
  });
});
