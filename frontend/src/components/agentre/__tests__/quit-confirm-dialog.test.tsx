import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { beforeEach, describe, expect, it, onTestFinished, vi } from "vitest";

import { presetForKey } from "@agentre-hub/agentre-ui";

import { useChatAgentsStore } from "@/stores/chat-agents-store";
import { useSessionMetaStore } from "@/stores/session-meta-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { QuitConfirmDialog } from "../quit-confirm-dialog";

import type { AgentStatus } from "@/stores/types";

const runtimeMocks = vi.hoisted(() => ({
  EventsOn: vi.fn(() => vi.fn()),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => runtimeMocks);

function installConfirmQuit() {
  const ConfirmQuit = vi.fn(() => Promise.resolve());
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { app: { App: { ConfirmQuit } } },
  });
  return ConfirmQuit;
}

function emitQuitBlocked(count: number) {
  const calls = runtimeMocks.EventsOn.mock.calls as unknown as Array<
    [string, (p: { count: number }) => void]
  >;
  const entry = calls.find((c) => c[0] === "app:quit-blocked");
  if (!entry) throw new Error("app:quit-blocked handler was never registered");
  act(() => entry[1]({ count }));
}

function seedSession(
  id: number,
  status: AgentStatus,
  agentName: string,
  title: string,
) {
  useSessionStatusStore.getState().upsert(id, {
    agentStatus: status,
    needsAttention: status === "waiting",
  });
  useSessionMetaStore.getState().setMeta(id, {
    agentId: id,
    agentName,
    agentColor: "agent-1",
    title,
  });
}

beforeEach(() => {
  useSessionStatusStore.getState().__reset();
  useSessionMetaStore.getState().__reset();
  runtimeMocks.EventsOn.mockReset();
  runtimeMocks.EventsOn.mockImplementation(() => vi.fn());
});

describe("QuitConfirmDialog", () => {
  it("stays closed until a quit-blocked event arrives", () => {
    installConfirmQuit();
    render(<QuitConfirmDialog />);
    expect(screen.queryByRole("dialog")).toBeNull();
  });

  it("opens and lists the in-progress sessions when quit is blocked", async () => {
    installConfirmQuit();
    seedSession(1, "running", "Backend refactor", "Drop group table");
    seedSession(2, "waiting", "Frontend i18n", "Proofread copy");
    render(<QuitConfirmDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());

    emitQuitBlocked(2);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText("Backend refactor")).toBeInTheDocument();
    expect(screen.getByText("Frontend i18n")).toBeInTheDocument();
  });

  it("quits when the user confirms", async () => {
    const ConfirmQuit = installConfirmQuit();
    seedSession(1, "running", "Backend refactor", "Drop group table");
    render(<QuitConfirmDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQuitBlocked(1);

    fireEvent.click(
      await screen.findByRole("button", { name: /quit anyway/i }),
    );

    expect(ConfirmQuit).toHaveBeenCalledTimes(1);
  });

  it("closes without quitting when the user cancels", async () => {
    const ConfirmQuit = installConfirmQuit();
    seedSession(1, "running", "Backend refactor", "Drop group table");
    render(<QuitConfirmDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());
    emitQuitBlocked(1);
    expect(await screen.findByRole("dialog")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /cancel/i }));

    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
    expect(ConfirmQuit).not.toHaveBeenCalled();
  });

  it("caps the visible list at three rows and summarizes the rest", async () => {
    installConfirmQuit();
    for (let i = 1; i <= 6; i++) {
      seedSession(i, "running", `Agent ${i}`, `Task ${i}`);
    }
    render(<QuitConfirmDialog />);
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());

    emitQuitBlocked(6);

    expect(await screen.findByRole("dialog")).toBeInTheDocument();
    expect(screen.getByText(/3 more/i)).toBeInTheDocument();
  });
});

// 规格 2026-11-02 决策 17：退出确认里每一行的 Agent 记号此前只画首字母 + 颜色，
// 现在按上传图片 ▸ 预设 ▸ 图标 ▸ 首字母显示完整身份（meta 里只有名字与颜色）。
describe("QuitConfirmDialog · Agent 记号显示完整身份", () => {
  function renderRowAvatar(identity: {
    avatarIcon?: string;
    avatarDataUrl?: string;
    avatarColor?: string;
    name?: string;
  }) {
    useChatAgentsStore.setState({
      agents: [
        {
          id: 1,
          name: "Backend",
          avatarColor: "agent-1",
          avatarIcon: "",
          avatarDataUrl: "",
          sessionIds: [],
          ...identity,
        },
      ] as never,
    });
    onTestFinished(() => useChatAgentsStore.getState().__reset());
    installConfirmQuit();
    seedSession(1, "running", "Backend", "Drop group table");

    render(<QuitConfirmDialog />);
    emitQuitBlocked(1);

    return within(screen.getByRole("dialog")).getByRole("img", {
      name: identity.name ?? "Backend",
    });
  }

  it("上传图片：记号画的是图片", () => {
    const avatar = renderRowAvatar({
      avatarDataUrl: "data:image/png;base64,AAAA",
    });

    expect(avatar.querySelector("img")?.getAttribute("src")).toBe(
      "data:image/png;base64,AAAA",
    );
  });

  it("预设 key：记号画注册表里的预设图", () => {
    const avatar = renderRowAvatar({ avatarIcon: "preset:agent-assistant" });

    expect(avatar.querySelector("img")?.getAttribute("src")).toBe(
      presetForKey("preset:agent-assistant")?.src,
    );
  });

  it("图标 key：记号画词表里的图标，而不是首字母", () => {
    const avatar = renderRowAvatar({ avatarIcon: "hammer" });

    expect(avatar.querySelector("svg")).not.toBeNull();
    expect(avatar).not.toHaveTextContent("B");
  });

  it("Given stale session metadata When quit is blocked Then the row uses the live agent name and colour", () => {
    const avatar = renderRowAvatar({ avatarColor: "agent-7", name: "Renamed" });
    expect(avatar.style.backgroundColor).toBe("var(--agent-7)");
    expect(screen.getByText("Renamed")).toBeInTheDocument();
    expect(screen.queryByText("Backend")).not.toBeInTheDocument();
  });

  it("Given an agent without a colour When quit is blocked Then the session metadata colour remains the fallback", () => {
    expect(renderRowAvatar({ avatarColor: "" }).style.backgroundColor).toBe(
      "var(--agent-1)",
    );
  });

  it("没有图标：记号照旧画首字母", () => {
    const avatar = renderRowAvatar({});

    expect(avatar).toHaveTextContent("B");
  });
});
