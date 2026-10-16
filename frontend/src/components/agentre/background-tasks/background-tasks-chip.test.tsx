import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BackgroundTasksChip } from "./background-tasks-chip";
import { BackgroundTasksPopoverContent } from "./background-tasks-popover";
import type { BackgroundTask } from "./types";

// 任何用 vi.useFakeTimers() 的用例若断言抛错,真实计时器不会被恢复,会泄漏到
// 后续测试。统一在 afterEach 兜底恢复。
afterEach(() => {
  vi.useRealTimers();
});

const running: BackgroundTask = {
  toolUseId: "tu1",
  kind: "local_bash",
  description: "sleep 20",
  status: "running",
};

const completed: BackgroundTask = {
  toolUseId: "tu2",
  kind: "local_bash",
  description: "Explore repo",
  status: "completed",
};

const failed: BackgroundTask = {
  toolUseId: "tu3",
  kind: "local_bash",
  description: "build step",
  status: "failed",
};

const runningAgent: BackgroundTask = {
  toolUseId: "ag1",
  taskId: "a1",
  kind: "local_agent",
  description: "Trace the stream",
  status: "running",
  startedAt: 1700000000000,
  subagentType: "Explore",
  model: "claude-sonnet-4-5",
  toolUses: 12,
};

const openChip = () =>
  userEvent.click(screen.getByRole("button", { name: /background tasks/i }));

describe("BackgroundTasksChip", () => {
  it("Given nothing is running but some tasks ended, When rendered, Then nothing is shown", () => {
    const { container } = render(
      <BackgroundTasksChip
        tasks={[completed, { ...failed, status: "canceled" }]}
      />,
    );
    expect(container).toBeEmptyDOMElement();
  });

  it("renders null when there are no tasks at all", () => {
    const { container } = render(<BackgroundTasksChip tasks={[]} />);
    expect(container).toBeEmptyDOMElement();
  });

  it("Given a running task, When rendered, Then it is an icon-only button whose name carries the running count and a status dot", () => {
    render(<BackgroundTasksChip tasks={[running, completed]} />);
    const btn = screen.getByRole("button", {
      name: "Background tasks: 1 running",
    });
    expect(btn).toHaveTextContent("");
    expect(within(btn).getByTestId("background-tasks-dot")).toBeInTheDocument();
  });

  it("Given a running task, When rendered, Then the button is transparent until hovered and its dot breathes slowly unless motion is reduced", () => {
    render(<BackgroundTasksChip tasks={[running]} />);
    const btn = screen.getByRole("button", {
      name: "Background tasks: 1 running",
    });
    expect(btn).toHaveAttribute("data-variant", "ghost");
    expect(within(btn).getByTestId("background-tasks-dot")).toHaveClass(
      "animate-pulse",
      "motion-reduce:animate-none",
    );
  });

  it("Given a running sub-agent, When peeked, Then its row lists type, steps, model and elapsed time", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(runningAgent.startedAt! + 65_000);
    render(<BackgroundTasksChip tasks={[runningAgent]} />);
    await userEvent.hover(screen.getByRole("button"));
    const peek = await screen.findByTestId("background-tasks-peek");
    const row = within(peek).getByText("Trace the stream").closest("li")!;
    expect(row).toHaveTextContent("Explore");
    expect(row).toHaveTextContent("12 steps");
    expect(row).toHaveTextContent("sonnet-4-5");
    expect(row).toHaveTextContent("1m 05s");
  });

  it("Given a running command, When peeked, Then its row shows how long it has run", async () => {
    vi.useFakeTimers({ toFake: ["Date"] });
    vi.setSystemTime(1700000000000 + 12_000);
    render(
      <BackgroundTasksChip
        tasks={[{ ...running, startedAt: 1700000000000 }]}
      />,
    );
    await userEvent.hover(screen.getByRole("button"));
    const peek = await screen.findByTestId("background-tasks-peek");
    expect(within(peek).getByText("sleep 20").closest("li")).toHaveTextContent(
      "12s",
    );
  });

  it("Given several running tasks, When rendered, Then the accessible name counts only the running ones", () => {
    const running2: BackgroundTask = { ...running, toolUseId: "tu4" };
    render(<BackgroundTasksChip tasks={[running, running2, completed]} />);
    expect(
      screen.getByRole("button", { name: "Background tasks: 2 running" }),
    ).toBeInTheDocument();
  });

  it("Given sub-agents and commands running, When hovered, Then the peek lists both kinds and the ended count", async () => {
    render(
      <BackgroundTasksChip
        tasks={[runningAgent, running, completed, failed]}
      />,
    );
    await userEvent.hover(screen.getByRole("button"));
    const peek = await screen.findByTestId("background-tasks-peek");
    expect(within(peek).getByText("Trace the stream")).toBeInTheDocument();
    expect(within(peek).getByText("sleep 20")).toBeInTheDocument();
    expect(within(peek).getByText(/Subagents/)).toBeInTheDocument();
    expect(within(peek).getByText(/Commands/)).toBeInTheDocument();
    expect(within(peek).getByText(/2 ended/)).toBeInTheDocument();
    expect(within(peek).queryByText("Explore repo")).toBeNull();
  });

  it("Given a keyboard user, When the button is focused, Then the peek shows too", async () => {
    render(<BackgroundTasksChip tasks={[running]} />);
    await userEvent.tab();
    expect(screen.getByRole("button")).toHaveFocus();
    expect(
      await screen.findByTestId("background-tasks-peek"),
    ).toBeInTheDocument();
  });

  it.each([
    ["visibility:hidden", { visibility: "hidden" }],
    ["display:none", { display: "none" }],
  ])(
    "Given the peek is open from keyboard focus, When the page holding the button is hidden (%s), Then the peek closes instead of floating detached",
    async (_name, hiddenStyle) => {
      render(
        <div data-testid="page">
          <BackgroundTasksChip tasks={[running]} />
        </div>,
      );
      await userEvent.tab();
      expect(
        await screen.findByTestId("background-tasks-peek"),
      ).toBeInTheDocument();

      Object.assign(screen.getByTestId("page").style, hiddenStyle);

      await waitFor(() =>
        expect(screen.queryByTestId("background-tasks-peek")).toBeNull(),
      );
    },
  );

  it("Given the peek is open from keyboard focus, When focus moves to an element outside, Then the peek closes", async () => {
    render(
      <>
        <BackgroundTasksChip tasks={[running]} />
        <button type="button">elsewhere</button>
      </>,
    );
    await userEvent.tab();
    expect(
      await screen.findByTestId("background-tasks-peek"),
    ).toBeInTheDocument();
    await userEvent.tab();
    await waitFor(() =>
      expect(screen.queryByTestId("background-tasks-peek")).toBeNull(),
    );
  });

  it("Given more than 4 commands running, When peeked, Then the first 4 show with a 'N more' line", async () => {
    const many = Array.from({ length: 6 }, (_, i) => ({
      ...running,
      toolUseId: `c${i}`,
      description: `cmd-${i}`,
    }));
    render(<BackgroundTasksChip tasks={many} />);
    await userEvent.hover(screen.getByRole("button"));
    const peek = await screen.findByTestId("background-tasks-peek");
    expect(within(peek).getByText("cmd-3")).toBeInTheDocument();
    expect(within(peek).queryByText("cmd-4")).toBeNull();
    expect(within(peek).getByText("2 more commands")).toBeInTheDocument();
  });

  it("Given exactly 4 commands, When peeked, Then there is no 'more' line", async () => {
    const four = Array.from({ length: 4 }, (_, i) => ({
      ...running,
      toolUseId: `c${i}`,
      description: `cmd-${i}`,
    }));
    render(<BackgroundTasksChip tasks={four} />);
    await userEvent.hover(screen.getByRole("button"));
    const peek = await screen.findByTestId("background-tasks-peek");
    expect(within(peek).queryByText(/more commands/)).toBeNull();
  });

  it("Given a located task, When its row is clicked, Then the host is told which task and the popover closes", async () => {
    const onLocate = vi.fn();
    const task = { ...running, messageId: 42 };
    render(<BackgroundTasksChip tasks={[task]} onLocateTask={onLocate} />);
    await openChip();
    await userEvent.click(screen.getByRole("button", { name: /sleep 20/ }));
    expect(onLocate).toHaveBeenCalledTimes(1);
    expect(onLocate).toHaveBeenCalledWith(task);
    // 对话滚过去之后弹层不该还盖在上面。
    expect(screen.queryByText("sleep 20")).toBeNull();
  });
});

describe("BackgroundTasksChip — hiding when nothing runs", () => {
  // 按钮只在有任务在跑时存在；最后一个任务在弹层开着时结束，按钮连同弹层一起消失。
  // 下一次有任务开跑时按钮回来，但弹层不能自己弹开——那次「开着」早已无人看见。
  it("Given the popover is open, When the last running task ends and a new one starts later, Then the button returns closed", async () => {
    const { rerender } = render(<BackgroundTasksChip tasks={[running]} />);
    await openChip();
    expect(
      screen.getByRole("region", { name: "Commands" }),
    ).toBeInTheDocument();

    const ended = { ...running, status: "completed" as const };
    rerender(<BackgroundTasksChip tasks={[ended]} />);
    expect(
      screen.queryByRole("button", { name: /background tasks/i }),
    ).toBeNull();

    rerender(
      <BackgroundTasksChip
        tasks={[
          ended,
          { ...running, toolUseId: "tu5", description: "sleep 30" },
        ]}
      />,
    );
    expect(
      screen.getByRole("button", { name: /background tasks/i }),
    ).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Commands" })).toBeNull();
    expect(screen.queryByTestId("background-tasks-peek")).toBeNull();
  });
});

describe("BackgroundTasksPopoverContent — running tasks are grouped by kind", () => {
  it("Given running subagents and commands, When shown, Then each kind has its own section with its running count", () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[
          runningAgent,
          { ...runningAgent, toolUseId: "ag2", description: "Reproduce it" },
          running,
        ]}
      />,
    );
    const agents = screen.getByRole("region", { name: "Subagents" });
    expect(within(agents).getByText("2 running")).toBeInTheDocument();
    expect(within(agents).getByText("Trace the stream")).toBeInTheDocument();
    expect(within(agents).getByText("Reproduce it")).toBeInTheDocument();
    expect(within(agents).queryByText("sleep 20")).toBeNull();

    const commands = screen.getByRole("region", { name: "Commands" });
    expect(within(commands).getByText("1 running")).toBeInTheDocument();
    expect(within(commands).getByText("sleep 20")).toBeInTheDocument();
  });

  it("Given only a running command, When shown, Then there is no subagent section", () => {
    render(<BackgroundTasksPopoverContent tasks={[running]} />);
    expect(screen.queryByRole("region", { name: "Subagents" })).toBeNull();
    expect(
      screen.getByRole("region", { name: "Commands" }),
    ).toBeInTheDocument();
  });
});

describe("BackgroundTasksPopoverContent — ended tasks sit behind one row", () => {
  it("Given running and ended tasks, When shown, Then ended tasks are counted on one row instead of listed", () => {
    render(
      <BackgroundTasksPopoverContent tasks={[running, completed, failed]} />,
    );
    expect(screen.getByText("sleep 20")).toBeInTheDocument();
    expect(screen.queryByText("Explore repo")).toBeNull();
    expect(screen.queryByText("build step")).toBeNull();
    expect(screen.getByRole("button", { name: /ended/i })).toHaveTextContent(
      "2",
    );
  });

  it("Given only running tasks, When shown, Then there is no ended row", () => {
    render(<BackgroundTasksPopoverContent tasks={[running]} />);
    expect(screen.queryByRole("button", { name: /ended/i })).toBeNull();
  });

  it("Given the ended row is opened, When shown, Then ended tasks are listed newest first and running ones are gone", async () => {
    render(
      <BackgroundTasksPopoverContent tasks={[running, completed, failed]} />,
    );
    await userEvent.click(screen.getByRole("button", { name: /ended/i }));
    expect(screen.queryByText("sleep 20")).toBeNull();
    const rows = screen.getAllByRole("listitem");
    // tasks 按发起顺序给进来;已结束页把最近的放最上面。
    expect(rows[0]).toHaveTextContent("build step");
    expect(rows[1]).toHaveTextContent("Explore repo");
  });

  it("Given the ended page, When Back is clicked, Then the running tasks are shown again", async () => {
    render(<BackgroundTasksPopoverContent tasks={[running, completed]} />);
    await userEvent.click(screen.getByRole("button", { name: /ended/i }));
    await userEvent.click(screen.getByRole("button", { name: "Back" }));
    expect(screen.getByText("sleep 20")).toBeInTheDocument();
    expect(screen.queryByText("Explore repo")).toBeNull();
  });

  it("Given nothing is running, When shown, Then the ended tasks are listed straight away with no way back", () => {
    render(<BackgroundTasksPopoverContent tasks={[completed, failed]} />);
    expect(screen.getByText("Explore repo")).toBeInTheDocument();
    expect(screen.getByText("build step")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Back" })).toBeNull();
  });

  it("Given the ended page, When Clear is clicked, Then the host is asked to clear and the page falls back to the running tasks once they are gone", async () => {
    const onClear = vi.fn();
    const { rerender } = render(
      <BackgroundTasksPopoverContent
        tasks={[running, completed]}
        onClearCompleted={onClear}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: /ended/i }));
    await userEvent.click(screen.getByRole("button", { name: "Clear" }));
    expect(onClear).toHaveBeenCalledTimes(1);

    // 宿主清掉之后只剩运行中的:不能停在一张空的「已结束」页上。
    rerender(
      <BackgroundTasksPopoverContent
        tasks={[running]}
        onClearCompleted={onClear}
      />,
    );
    expect(screen.getByText("sleep 20")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Back" })).toBeNull();
  });

  it("Given the host cannot clear, When the ended page is shown, Then there is no Clear button", () => {
    render(<BackgroundTasksPopoverContent tasks={[completed]} />);
    expect(screen.queryByRole("button", { name: "Clear" })).toBeNull();
  });
});

describe("BackgroundTasksPopoverContent — what a row says", () => {
  it("Given a running subagent, When shown, Then the row gives its type, steps and short model name but not its task id", () => {
    render(<BackgroundTasksPopoverContent tasks={[runningAgent]} />);
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("Explore")).toBeInTheDocument();
    expect(within(row).getByText("12 steps")).toBeInTheDocument();
    expect(within(row).getByText("sonnet-4-5")).toBeInTheDocument();
    expect(within(row).queryByTestId("task-id")).toBeNull();
  });

  it("Given a subagent that reported no type, model or step, When shown, Then the row falls back to the generic subagent label and shows nothing made up", () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[
          {
            toolUseId: "ag0",
            kind: "local_agent",
            description: "Explore repo",
            status: "running",
          },
        ]}
      />,
    );
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("subagent")).toBeInTheDocument();
    expect(within(row).queryByText(/steps/)).toBeNull();
  });

  it("Given a background command, When shown, Then the row is labelled bash and keeps its task id", () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[{ ...running, taskId: "b3875slp0" }]}
      />,
    );
    const row = screen.getByRole("listitem");
    expect(within(row).getByText("bash")).toBeInTheDocument();
    expect(within(row).getByTestId("task-id")).toHaveTextContent("b3875slp0");
  });

  it("Given tasks in each state, When shown, Then every row states its status", async () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[
          running,
          completed,
          failed,
          { ...failed, toolUseId: "tu9", status: "canceled" },
        ]}
      />,
    );
    expect(
      within(screen.getByRole("listitem")).getByText("Running"),
    ).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: /ended/i }));
    const [stopped, failedRow, done] = screen.getAllByRole("listitem");
    expect(within(stopped).getByText("Stopped")).toBeInTheDocument();
    expect(within(failedRow).getByText("Failed")).toBeInTheDocument();
    expect(within(done).getByText("Done")).toBeInTheDocument();
  });
});

describe("BackgroundTasksPopoverContent — locating a task in the conversation", () => {
  it("Given a task whose launching message is known, When its row is clicked, Then the host is asked to locate that task", async () => {
    const onLocate = vi.fn();
    const task = { ...running, messageId: 42 };
    render(
      <BackgroundTasksPopoverContent tasks={[task]} onLocateTask={onLocate} />,
    );
    await userEvent.click(screen.getByRole("button", { name: /sleep 20/ }));
    expect(onLocate).toHaveBeenCalledWith(task);
  });

  it("Given a task still inside the live turn (no message yet), When shown, Then its row is not a button", () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[running]}
        onLocateTask={vi.fn()}
      />,
    );
    expect(screen.getByText("sleep 20")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /sleep 20/ })).toBeNull();
  });

  it("Given the host cannot locate, When shown, Then the row is not a button even with a known message", () => {
    render(
      <BackgroundTasksPopoverContent tasks={[{ ...running, messageId: 42 }]} />,
    );
    expect(screen.queryByRole("button", { name: /sleep 20/ })).toBeNull();
  });
});

describe("BackgroundTasksPopoverContent — elapsed + summary", () => {
  it("shows elapsed for a running task with startedAt", () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date(1700000030000)); // 30s after startedAt

    const tasks: BackgroundTask[] = [
      {
        toolUseId: "tu-r",
        kind: "local_bash",
        description: "sleep 20",
        status: "running",
        startedAt: 1700000000000,
      },
    ];
    render(<BackgroundTasksPopoverContent tasks={tasks} />);
    // running 30s → "30s"
    expect(screen.getByTestId("elapsed")).toHaveTextContent("30s");
  });

  it("shows frozen durationMs for a completed bash task", () => {
    const tasks: BackgroundTask[] = [
      {
        toolUseId: "tu-c",
        kind: "local_bash",
        description: "Explore",
        status: "completed",
        durationMs: 4200,
      },
    ];
    render(<BackgroundTasksPopoverContent tasks={tasks} />);
    expect(screen.getByTestId("elapsed")).toHaveTextContent("4s");
  });

  it("shows summary text for a completed bash task", () => {
    const summary = 'Background command "sleep 20" completed (exit code 0)';
    const tasks: BackgroundTask[] = [
      {
        toolUseId: "tu-b",
        kind: "local_bash",
        description: "sleep 20",
        status: "completed",
        summary,
      },
    ];
    render(<BackgroundTasksPopoverContent tasks={tasks} />);
    expect(screen.getByText(summary)).toBeInTheDocument();
  });

  it("shows only the title for a subagent task, not its report body", () => {
    // subagent summary 是子代理的完整回报正文,会把弹层撑得很高 —— 只展示标题。
    const summary =
      "Verification complete. Working tree untouched (hashes unchanged).";
    const tasks: BackgroundTask[] = [
      {
        toolUseId: "tu-sa",
        kind: "local_agent",
        description: "Verify validator against spec criteria",
        status: "completed",
        summary,
      },
    ];
    render(<BackgroundTasksPopoverContent tasks={tasks} />);
    expect(
      screen.getByText("Verify validator against spec criteria"),
    ).toBeInTheDocument();
    expect(screen.queryByText(summary)).toBeNull();
  });

  it("does not show elapsed for a running task without startedAt", () => {
    const tasks: BackgroundTask[] = [
      {
        toolUseId: "tu-no-start",
        kind: "local_bash",
        description: "sleep 20",
        status: "running",
      },
    ];
    render(<BackgroundTasksPopoverContent tasks={tasks} />);
    expect(screen.queryByTestId("elapsed")).toBeNull();
  });

  it("does not show elapsed for a stopped task even when a duration was recorded", () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[
          {
            toolUseId: "tu-x",
            kind: "local_bash",
            description: "watch",
            status: "canceled",
            durationMs: 9000,
          },
        ]}
      />,
    );
    expect(screen.queryByTestId("elapsed")).toBeNull();
  });

  it("does not show summary when summary is absent", () => {
    const tasks: BackgroundTask[] = [
      {
        toolUseId: "tu-no-summary",
        kind: "local_bash",
        description: "run",
        status: "completed",
      },
    ];
    render(<BackgroundTasksPopoverContent tasks={tasks} />);
    // No extra text beyond what's expected
    expect(screen.queryByText(/exit code/)).toBeNull();
  });

  it("formats minute-range frozen durationMs as m ss", () => {
    const tasks: BackgroundTask[] = [
      {
        toolUseId: "tu-min",
        kind: "local_bash",
        description: "task",
        status: "completed",
        durationMs: 185_000, // 3m 05s
      },
    ];
    render(<BackgroundTasksPopoverContent tasks={tasks} />);
    expect(screen.getByTestId("elapsed")).toHaveTextContent("3m 05s");
  });

  it("formats hour-range frozen durationMs as h mm", () => {
    const tasks: BackgroundTask[] = [
      {
        toolUseId: "tu-hr",
        kind: "local_bash",
        description: "task",
        status: "completed",
        durationMs: 3_720_000, // 1h 02m
      },
    ];
    render(<BackgroundTasksPopoverContent tasks={tasks} />);
    expect(screen.getByTestId("elapsed")).toHaveTextContent("1h 02m");
  });
});

describe("BackgroundTasksPopoverContent — stop button", () => {
  it("renders a Stop button for a running task with taskId and calls onStopTask", async () => {
    const onStop = vi.fn();
    const task: BackgroundTask = {
      toolUseId: "tu-r",
      taskId: "b0",
      kind: "local_bash",
      description: "sleep 20",
      status: "running",
    };
    render(
      <BackgroundTasksPopoverContent tasks={[task]} onStopTask={onStop} />,
    );
    await userEvent.click(screen.getByRole("button", { name: /停止|stop/i }));
    expect(onStop).toHaveBeenCalledTimes(1);
    expect(onStop).toHaveBeenCalledWith(task);
  });

  it("Given a located running task, When Stop is clicked, Then the task is stopped without also jumping to it", async () => {
    const onStop = vi.fn();
    const onLocate = vi.fn();
    render(
      <BackgroundTasksPopoverContent
        tasks={[{ ...running, taskId: "b0", messageId: 42 }]}
        onStopTask={onStop}
        onLocateTask={onLocate}
      />,
    );
    await userEvent.click(screen.getByRole("button", { name: "Stop" }));
    expect(onStop).toHaveBeenCalledTimes(1);
    expect(onLocate).not.toHaveBeenCalled();
  });

  it("does not render Stop when the running task has no taskId", () => {
    const onStop = vi.fn();
    render(
      <BackgroundTasksPopoverContent
        tasks={[
          {
            toolUseId: "tu-r",
            kind: "local_bash",
            description: "sleep 20",
            status: "running",
          },
        ]}
        onStopTask={onStop}
      />,
    );
    expect(screen.queryByRole("button", { name: /停止|stop/i })).toBeNull();
  });

  it("does not render Stop when onStopTask is absent", () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[
          {
            toolUseId: "tu-r",
            taskId: "b0",
            kind: "local_bash",
            description: "sleep 20",
            status: "running",
          },
        ]}
      />,
    );
    expect(screen.queryByRole("button", { name: /停止|stop/i })).toBeNull();
  });
});
