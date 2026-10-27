import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";

const navigate = vi.fn();

vi.mock("react-router-dom", () => ({ useNavigate: () => navigate }));

vi.mock("../../../wailsjs/go/app/App", () => ({
  AnswerToolApproval: vi.fn(),
  AnswerToolPermission: vi.fn(),
  AnswerUserQuestion: vi.fn(),
  OpenPath: vi.fn(),
  ResolveExecApproval: vi.fn(),
  ResolvePlanAction: vi.fn(),
  WorkspaceFsReadFile: vi.fn(),
}));

vi.mock("../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: vi.fn(),
}));

import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useDesktopTranscriptPorts } from "./transcript-ports-desktop";

beforeEach(() => {
  navigate.mockReset();
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
});

describe("useDesktopTranscriptPorts 的 openSession", () => {
  it("Given a child session id, When the port is called, Then the chat tab for it is opened and the chat route shown", () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    result.current.openSession?.(42);

    const { tabs, activeTabId } = useChatTabsStore.getState();
    const active = tabs.find((t) => t.id === activeTabId);
    expect(active?.meta).toEqual({ kind: "session", sessionId: 42 });
    expect(navigate).toHaveBeenCalledWith("/chat");
  });

  it("Given the session already has a tab, When the port is called again, Then that tab is reused", () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    result.current.openSession?.(42);
    result.current.openSession?.(42);

    expect(useChatTabsStore.getState().tabs).toHaveLength(1);
  });
});
