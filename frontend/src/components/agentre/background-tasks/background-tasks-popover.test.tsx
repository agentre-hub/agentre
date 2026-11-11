import { act, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { BackgroundTasksPopoverContent } from "./background-tasks-popover";
import type { BackgroundTask, SessionProcess } from "./types";

afterEach(() => {
  vi.useRealTimers();
});

const runningBash: BackgroundTask = {
  toolUseId: "tu1",
  kind: "local_bash",
  description: "sleep 20",
  status: "running",
};

const ended: BackgroundTask = {
  toolUseId: "tu2",
  kind: "local_bash",
  description: "old job",
  status: "completed",
};

const vite: SessionProcess = {
  pid: 48211,
  command: "node vite --port 5391",
  ports: [5391, 5392],
  startedAt: 1_000_000,
  cwd: "~/work/demo/frontend",
};

const tail: SessionProcess = {
  pid: 48377,
  command: "tail -f /tmp/backend.log",
  ports: [],
  startedAt: 1_000_000,
};

const processSection = () =>
  screen.getByRole("region", { name: "Processes & ports" });

describe("BackgroundTasksPopoverContent processes", () => {
  it("Given processes and a running task, When rendered, Then the processes section follows commands with a live count", () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[runningBash]}
        processes={[vite, tail]}
      />,
    );
    const regions = screen
      .getAllByRole("region")
      .map((r) => r.getAttribute("aria-label"));
    expect(regions).toEqual(["Commands", "Processes & ports"]);
    expect(processSection()).toHaveTextContent("2 running");
  });

  it("Given a process with ports, When rendered, Then the row shows command, ports, PID, elapsed and cwd", () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_000_000 + 65_000);
    render(<BackgroundTasksPopoverContent tasks={[]} processes={[vite]} />);
    const section = processSection();
    expect(within(section).getByText("node vite --port 5391")).toBeVisible();
    expect(section).toHaveTextContent(":5391 :5392");
    expect(section).toHaveTextContent("PID 48211");
    expect(section).toHaveTextContent("1m 05s");
    expect(section).toHaveTextContent("~/work/demo/frontend");
  });

  it("Given a live process, When a second passes, Then the elapsed time ticks", () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_000_000 + 5_000);
    render(<BackgroundTasksPopoverContent tasks={[]} processes={[vite]} />);
    expect(processSection()).toHaveTextContent("5s");
    act(() => {
      vi.advanceTimersByTime(2000);
    });
    expect(processSection()).toHaveTextContent("7s");
  });

  it("Given a process without ports or cwd, When rendered, Then it says no listening port and omits cwd", () => {
    render(<BackgroundTasksPopoverContent tasks={[]} processes={[tail]} />);
    const section = processSection();
    expect(section).toHaveTextContent("No listening port");
    expect(section).toHaveTextContent("PID 48377");
    expect(
      section.querySelectorAll("li [class*='font-mono'] > span"),
    ).toHaveLength(5);
  });

  // A5:多端口时每个端口各自是一颗按钮,打开的就是那个端口;行尾不再放含糊的图标键。
  it("Given a process with several ports, When a port button is clicked, Then that port opens and no row-end open button is offered", async () => {
    const onOpen = vi.fn();
    render(
      <BackgroundTasksPopoverContent
        tasks={[]}
        processes={[vite, tail]}
        onOpenProcess={onOpen}
        onKillProcess={vi.fn()}
      />,
    );
    expect(
      screen.queryByRole("button", { name: "Open in browser" }),
    ).toBeNull();
    await userEvent.click(
      screen.getByRole("button", { name: "Open :5392 in browser" }),
    );
    expect(onOpen).toHaveBeenCalledWith(vite, 5392);
    await userEvent.click(
      screen.getByRole("button", { name: "Open :5391 in browser" }),
    );
    expect(onOpen).toHaveBeenLastCalledWith(vite, 5391);
  });

  it("Given a process with one port, When the row-end open button is clicked, Then it opens that port", async () => {
    const onOpen = vi.fn();
    const single = { ...vite, ports: [5391] };
    render(
      <BackgroundTasksPopoverContent
        tasks={[]}
        processes={[single, tail]}
        onOpenProcess={onOpen}
      />,
    );
    const buttons = screen.getAllByRole("button", { name: "Open in browser" });
    expect(buttons).toHaveLength(1);
    await userEvent.click(buttons[0]);
    expect(onOpen).toHaveBeenCalledWith(single, 5391);
  });

  it("Given no callbacks, When rendered, Then no action buttons appear and rows are not clickable", () => {
    render(<BackgroundTasksPopoverContent tasks={[]} processes={[vite]} />);
    expect(screen.queryByRole("button")).toBeNull();
  });

  it("Given only processes, When rendered, Then only the processes section shows", () => {
    render(<BackgroundTasksPopoverContent tasks={[]} processes={[vite]} />);
    expect(screen.getAllByRole("region")).toHaveLength(1);
    expect(processSection()).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /ended/i })).toBeNull();
  });

  it("Given processes, a running task and ended tasks, When rendered, Then Ended stays pinned below the sections", () => {
    render(
      <BackgroundTasksPopoverContent
        tasks={[runningBash, ended]}
        processes={[vite]}
      />,
    );
    expect(screen.getByRole("button", { name: /ended/i })).toHaveTextContent(
      "1",
    );
    expect(processSection()).toBeInTheDocument();
  });

  it.each([[undefined], [[]]])(
    "Given processes is %j, When rendered, Then the section is absent",
    (processes) => {
      render(
        <BackgroundTasksPopoverContent
          tasks={[runningBash]}
          processes={processes}
        />,
      );
      expect(
        screen.queryByRole("region", { name: "Processes & ports" }),
      ).toBeNull();
    },
  );
});

// A4:结束进程不可恢复,两段式确认;执行中转圈,失败复原。
describe("BackgroundTasksPopoverContent kill process", () => {
  it("Given a process, When kill is clicked once, Then nothing runs and the button turns into a destructive Stop? confirm", async () => {
    const onKill = vi.fn();
    render(
      <BackgroundTasksPopoverContent
        tasks={[]}
        processes={[vite, tail]}
        onKillProcess={onKill}
      />,
    );
    const stops = screen.getAllByRole("button", { name: "Stop process" });
    expect(stops).toHaveLength(2);
    // 图标与「停止任务」的方块不同。
    expect(stops[1].querySelector("svg.lucide-x")).not.toBeNull();
    expect(stops[1].querySelector("svg.lucide-square")).toBeNull();
    await userEvent.click(stops[1]);
    expect(onKill).not.toHaveBeenCalled();
    const confirm = screen.getByRole("button", { name: "Stop?" });
    expect(confirm).toBe(stops[1]);
  });

  it("Given the confirm is showing, When clicked again within the window, Then onKillProcess gets that process and the button spins disabled", async () => {
    const onKill = vi.fn(() => new Promise<void>(() => {}));
    render(
      <BackgroundTasksPopoverContent
        tasks={[]}
        processes={[vite, tail]}
        onKillProcess={onKill}
      />,
    );
    const button = screen.getAllByRole("button", { name: "Stop process" })[1];
    await userEvent.click(button);
    await userEvent.click(button);
    expect(onKill).toHaveBeenCalledTimes(1);
    expect(onKill).toHaveBeenCalledWith(tail);
    expect(button).toBeDisabled();
    expect(button).toHaveAccessibleName("Stop process");
    expect(button.querySelector("svg.animate-spin")).not.toBeNull();
  });

  it("Given the confirm is showing, When about 2 seconds pass without a second click, Then it reverts and the next click only re-arms", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const onKill = vi.fn();
    const user = userEvent.setup({
      advanceTimers: vi.advanceTimersByTime.bind(vi),
    });
    render(
      <BackgroundTasksPopoverContent
        tasks={[]}
        processes={[tail]}
        onKillProcess={onKill}
      />,
    );
    const button = screen.getByRole("button", { name: "Stop process" });
    await user.click(button);
    expect(button).toHaveAccessibleName("Stop?");
    act(() => {
      vi.advanceTimersByTime(2100);
    });
    expect(button).toHaveAccessibleName("Stop process");
    await user.click(button);
    expect(onKill).not.toHaveBeenCalled();
    expect(button).toHaveAccessibleName("Stop?");
  });

  it("Given the kill fails, When it settles, Then the button returns to its resting state", async () => {
    let reject!: (e: Error) => void;
    const onKill = vi.fn(
      () =>
        new Promise<void>((_, r) => {
          reject = r;
        }),
    );
    render(
      <BackgroundTasksPopoverContent
        tasks={[]}
        processes={[tail]}
        onKillProcess={onKill}
      />,
    );
    const button = screen.getByRole("button", { name: "Stop process" });
    await userEvent.click(button);
    await userEvent.click(button);
    expect(button).toBeDisabled();
    await act(async () => {
      reject(new Error("denied"));
    });
    expect(button).toBeEnabled();
    expect(button).toHaveAccessibleName("Stop process");
    expect(button.querySelector("svg.lucide-x")).not.toBeNull();
  });
});

describe("BackgroundTasksPopoverContent process row", () => {
  it("Given a process row, When rendered, Then the row has no hover fill, the command wraps and is selectable, and cwd truncates from the head", () => {
    render(<BackgroundTasksPopoverContent tasks={[]} processes={[vite]} />);
    const section = processSection();
    const row = section.querySelector("li")!;
    expect(row.className).not.toMatch(/hover:bg-/);
    const command = within(section).getByText("node vite --port 5391");
    expect(command.className).not.toContain("truncate");
    expect(command.className).toContain("break-all");
    expect(command.closest("[data-selectable-text='true']")).not.toBeNull();
    const cwd = within(section).getByText("~/work/demo/frontend");
    const cwdBox = cwd.closest("[dir='rtl']");
    expect(cwdBox).not.toBeNull();
    expect(cwdBox!.className).toContain("truncate");
    expect(cwd.closest("[data-selectable-text='true']")).not.toBeNull();
  });
});

// A4:停止后台任务点下后转圈并禁用,直到状态变化或调用失败。
describe("BackgroundTasksPopoverContent stop task", () => {
  const runningWithId: BackgroundTask = { ...runningBash, taskId: "b3875slp0" };

  it("Given a running task, When stop is clicked, Then onStopTask gets it and the button spins disabled while pending", async () => {
    const onStop = vi.fn(() => new Promise<void>(() => {}));
    render(
      <BackgroundTasksPopoverContent
        tasks={[runningWithId]}
        onStopTask={onStop}
      />,
    );
    const button = screen.getByRole("button", { name: "Stop" });
    await userEvent.click(button);
    await userEvent.click(button);
    expect(onStop).toHaveBeenCalledTimes(1);
    expect(onStop).toHaveBeenCalledWith(runningWithId);
    expect(button).toBeDisabled();
    expect(button.querySelector("svg.animate-spin")).not.toBeNull();
    expect(button.className).toContain("size-6");
  });

  it("Given the stop fails, When it settles, Then the stop button is usable again", async () => {
    let reject!: (e: Error) => void;
    const onStop = vi.fn(
      () =>
        new Promise<void>((_, r) => {
          reject = r;
        }),
    );
    render(
      <BackgroundTasksPopoverContent
        tasks={[runningWithId]}
        onStopTask={onStop}
      />,
    );
    const button = screen.getByRole("button", { name: "Stop" });
    await userEvent.click(button);
    expect(button).toBeDisabled();
    await act(async () => {
      reject(new Error("missing task id"));
    });
    expect(button).toBeEnabled();
    expect(button.querySelector("svg.animate-spin")).toBeNull();
  });
});
