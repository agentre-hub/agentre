import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const appMocks = vi.hoisted(() => ({
  ListAgentExecTargetAvailability: vi.fn(),
  ListAgentBackends: vi.fn(),
  ServerListDevices: vi.fn(),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => appMocks);
vi.mock("../../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: () => () => {},
}));

import { BoardExecTargetPill } from "../exec-target-pill";

function stubCandidates() {
  appMocks.ListAgentExecTargetAvailability.mockResolvedValue([
    {
      agentBackendId: 11,
      available: true,
      reason: "",
      hint: "",
      projectPath: "/Users/me/code/agentre",
      kind: "local",
    },
    {
      agentBackendId: 22,
      available: false,
      reason: "exec-target-offline",
      hint: "",
      projectPath: "/srv/agentre",
      kind: "daemon",
    },
  ]);
  appMocks.ListAgentBackends.mockResolvedValue({
    items: [
      {
        id: 11,
        type: "claudecode",
        name: "本机 Claude",
        deviceId: "",
        online: true,
      },
      {
        id: 22,
        type: "codex",
        name: "构建机 Codex",
        deviceId: "fp-2",
        deviceName: "构建机",
        online: false,
      },
    ],
  });
  appMocks.ServerListDevices.mockResolvedValue([]);
}

describe("BoardExecTargetPill", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    stubCandidates();
  });

  it("Given no selection, When the pill renders, Then it says the task follows the agent binding", async () => {
    render(
      <BoardExecTargetPill
        className="pill"
        agentId={5}
        projectId={3}
        backendId={null}
        onChange={() => {}}
      />,
    );

    expect(
      await screen.findByTestId("board-exec-target-pill"),
    ).toHaveTextContent("Follow agent binding");
  });

  it("Given the picker is opened, When candidates render, Then each shows its kind, backend type, machine and this project's path there", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    render(
      <BoardExecTargetPill
        className="pill"
        agentId={5}
        projectId={3}
        backendId={null}
        onChange={() => {}}
      />,
    );

    await user.click(await screen.findByTestId("board-exec-target-pill"));

    const local = await screen.findByTestId("board-exec-target-row-11");
    expect(local).toHaveTextContent("This computer");
    expect(local).toHaveTextContent("Claude Code");
    expect(local).toHaveTextContent("/Users/me/code/agentre");
  });

  it("Given an unavailable candidate, When the picker renders, Then it stays visible with its reason and cannot be chosen", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onChange = vi.fn();
    render(
      <BoardExecTargetPill
        className="pill"
        agentId={5}
        projectId={3}
        backendId={null}
        onChange={onChange}
      />,
    );

    await user.click(await screen.findByTestId("board-exec-target-pill"));
    const offline = await screen.findByTestId("board-exec-target-row-22");

    expect(offline).toHaveTextContent("Offline");
    expect(offline).toBeDisabled();
  });

  it("Given a candidate is picked, When it is clicked, Then the host is told which backend won", async () => {
    const user = userEvent.setup({ pointerEventsCheck: 0 });
    const onChange = vi.fn();
    render(
      <BoardExecTargetPill
        className="pill"
        agentId={5}
        projectId={3}
        backendId={null}
        onChange={onChange}
      />,
    );

    await user.click(await screen.findByTestId("board-exec-target-pill"));
    await user.click(await screen.findByTestId("board-exec-target-row-11"));

    expect(onChange).toHaveBeenCalledWith(11);
  });

  it("Given a selection that is no longer among the candidates, When they resolve, Then it falls back to the agent binding", async () => {
    const onChange = vi.fn();
    render(
      <BoardExecTargetPill
        className="pill"
        agentId={5}
        projectId={3}
        backendId={99}
        onChange={onChange}
      />,
    );

    await waitFor(() => expect(onChange).toHaveBeenCalledWith(null));
  });

  it("Given a chosen machine, When its candidate resolves, Then the host is told that machine's backend type", async () => {
    const onResolvedBackendType = vi.fn();
    render(
      <BoardExecTargetPill
        className="pill"
        agentId={5}
        projectId={3}
        backendId={11}
        onChange={() => {}}
        onResolvedBackendType={onResolvedBackendType}
      />,
    );

    // 模型那一颗要用**生效档**的 backendType 过兼容判据，不是 Agent 自己绑的那个。
    await waitFor(() =>
      expect(onResolvedBackendType).toHaveBeenLastCalledWith("claudecode"),
    );
  });

  it("Given the candidates never loaded, When the pill renders, Then the selection is left alone", async () => {
    appMocks.ListAgentExecTargetAvailability.mockRejectedValue(new Error("no"));
    const onChange = vi.fn();
    render(
      <BoardExecTargetPill
        className="pill"
        agentId={5}
        projectId={3}
        backendId={99}
        onChange={onChange}
      />,
    );

    await waitFor(() =>
      expect(appMocks.ListAgentExecTargetAvailability).toHaveBeenCalled(),
    );
    expect(onChange).not.toHaveBeenCalled();
  });
});
