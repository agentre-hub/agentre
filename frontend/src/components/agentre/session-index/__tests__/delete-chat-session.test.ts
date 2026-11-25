import { beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({
  DeleteChatSession: vi.fn(),
  TerminalClose: vi.fn(),
}));
vi.mock("../../../../../wailsjs/go/app/App", () => api);
vi.mock("@/../wailsjs/go/app/App", () => api);

import { useSessionRootStore } from "@/stores/session-root-store";

import { useWebPreviewStore } from "../../tool-windows/web-preview-store";
import { deleteChatSession } from "../delete-chat-session";

const CWD = "/work/demo-app";

function seed(sessionId: number) {
  useSessionRootStore.getState().applyRoots(sessionId, CWD, [
    {
      path: CWD,
      name: "demo-app",
      isPrimary: true,
      isWorktree: false,
      aiHere: false,
    },
  ]);
  useWebPreviewStore
    .getState()
    .syncPorts(sessionId, [{ port: 3000, command: "vite" }]);
}

beforeEach(() => {
  useSessionRootStore.setState({ bySession: {} });
  useWebPreviewStore.setState({ bySession: {} });
  api.DeleteChatSession.mockReset();
});

describe("deleteChatSession", () => {
  // Given 会话 7 与 8 都有工作根状态与网页预览标签,
  // When 删除会话 7,
  // Then 7 的那些会话级状态一并清掉，8 的不动。
  it("drops the deleted session's work-root and web-preview state only", async () => {
    seed(7);
    seed(8);
    api.DeleteChatSession.mockResolvedValueOnce(undefined);

    await deleteChatSession(7);

    expect(useSessionRootStore.getState().bySession[7]).toBeUndefined();
    expect(useWebPreviewStore.getState().bySession[7]).toBeUndefined();
    expect(useSessionRootStore.getState().bySession[8]).toBeDefined();
    expect(useWebPreviewStore.getState().bySession[8]?.tabs).toHaveLength(1);
  });

  // Given 删除失败, Then 会话还在，它的状态照留。
  it("keeps the state when the delete fails", async () => {
    seed(7);
    api.DeleteChatSession.mockRejectedValueOnce(new Error("nope"));

    await expect(deleteChatSession(7)).rejects.toThrow("nope");

    expect(useSessionRootStore.getState().bySession[7]).toBeDefined();
    expect(useWebPreviewStore.getState().bySession[7]).toBeDefined();
  });
});
