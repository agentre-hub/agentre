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

  it("Given handlers, When the open button is clicked, Then only rows with ports offer it and it passes that process", async () => {
    const onOpen = vi.fn();
    render(
      <BackgroundTasksPopoverContent
        tasks={[]}
        processes={[vite, tail]}
        onOpenProcess={onOpen}
        onKillProcess={vi.fn()}
      />,
    );
    const buttons = screen.getAllByRole("button", {
      name: "Open in browser",
    });
    expect(buttons).toHaveLength(1);
    await userEvent.click(buttons[0]);
    expect(onOpen).toHaveBeenCalledWith(vite);
  });

  it("Given handlers, When stop is clicked on a row, Then onKillProcess gets that process", async () => {
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
    await userEvent.click(stops[1]);
    expect(onKill).toHaveBeenCalledWith(tail);
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
