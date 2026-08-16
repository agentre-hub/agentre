import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { LocalCommandsProvider } from "./access";
import {
  createFakeLocalCommands,
  type FakeLocalCommands,
} from "./__testing__/fake-local-commands";
import { LocalCommandCard } from "./card";

// Output is rendered by a read-only xterm; stub it so this test stays focused on
// card chrome (status / buttons / dismiss). Terminal rendering is covered by
// output-terminal.test.tsx.
vi.mock("./output-terminal", () => ({ OutputTerminal: () => null }));

let commands: FakeLocalCommands;

function renderCard(ui: React.ReactElement) {
  return render(
    <LocalCommandsProvider access={commands.access}>
      {ui}
    </LocalCommandsProvider>,
  );
}

describe("LocalCommandCard", () => {
  beforeEach(() => {
    commands = createFakeLocalCommands();
  });

  it("Given a running command, When Stop is clicked, Then the card delegates the terminal id without owning settlement", async () => {
    const onStop = vi.fn();
    commands.start({ id: "t1", command: "go test" });
    renderCard(
      <LocalCommandCard
        entryId="t1"
        onOpenInTerminal={vi.fn()}
        onStop={onStop}
      />,
    );

    await userEvent.click(screen.getByRole("button", { name: /Stop/ }));

    expect(onStop).toHaveBeenCalledTimes(1);
    expect(onStop).toHaveBeenCalledWith("t1");
    expect(commands.get("t1")?.status).toBe("running");
  });

  it("Given a read-only caller without onStop, When a command is running, Then the card omits the Stop control", () => {
    commands.start({ id: "t-readonly", command: "sleep 30" });
    renderCard(
      <LocalCommandCard entryId="t-readonly" onOpenInTerminal={vi.fn()} />,
    );

    expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();
    expect(
      screen.getByRole("button", { name: /Open in terminal/ }),
    ).toBeInTheDocument();
  });

  it("after exit shows exit code and no run-time action buttons", () => {
    commands.start({ id: "t2", command: "go test" });
    commands.finish("t2", "failed", 1);
    renderCard(<LocalCommandCard entryId="t2" onOpenInTerminal={vi.fn()} />);

    expect(screen.getByText(/Exit 1/)).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: /Stop/ })).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Open in terminal/ }),
    ).toBeNull();
  });

  it("running shows no dismiss button (must stop first)", () => {
    commands.start({ id: "t3", command: "sleep 1" });
    renderCard(<LocalCommandCard entryId="t3" onOpenInTerminal={vi.fn()} />);

    expect(screen.queryByRole("button", { name: /Dismiss/ })).toBeNull();
  });

  it("after finish a dismiss button removes the card from the host", async () => {
    commands.start({ id: "t4", command: "echo hi" });
    commands.finish("t4", "done", 0);
    renderCard(<LocalCommandCard entryId="t4" onOpenInTerminal={vi.fn()} />);
    expect(screen.getByText("echo hi")).toBeInTheDocument();

    await userEvent.click(screen.getByRole("button", { name: /Dismiss/ }));

    expect(commands.get("t4")).toBeUndefined();
    expect(screen.queryByText("echo hi")).toBeNull();
  });

  it("finished command defaults to a collapsed one-line summary (no chip / no 'not sent to AI')", () => {
    commands.start({ id: "s1", command: "git status", createdAt: 1000 });
    commands.finish("s1", "done", 0); // fake 的 finishedAt=2200 → 1.2s
    renderCard(<LocalCommandCard entryId="s1" onOpenInTerminal={vi.fn()} />);

    expect(screen.getByText("git status")).toBeInTheDocument();
    expect(screen.getByText("1.2s")).toBeInTheDocument();
    expect(screen.getByText(/Exit 0/)).toBeInTheDocument();
    // 折叠行不含 chip 与 "不发送给 AI"
    expect(screen.queryByText(/Local command/)).toBeNull();
    expect(screen.queryByText(/Not sent to AI/)).toBeNull();
  });

  it("clicking a collapsed summary expands it to reveal the header chip", async () => {
    commands.start({ id: "s2", command: "ls", createdAt: 1000 });
    commands.finish("s2", "done", 0);
    renderCard(<LocalCommandCard entryId="s2" onOpenInTerminal={vi.fn()} />);
    // collapsed: no chip yet
    expect(screen.queryByText(/Local command/)).toBeNull();

    await userEvent.click(
      screen.getByRole("button", { name: /Expand output/ }),
    );

    // expanded header now shows chip + "not sent to AI"
    expect(screen.getByText(/Local command/)).toBeInTheDocument();
    expect(screen.getByText(/Not sent to AI/)).toBeInTheDocument();
  });
});
