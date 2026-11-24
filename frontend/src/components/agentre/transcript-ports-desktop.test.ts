import { beforeEach, describe, expect, it, vi } from "vitest";
import { renderHook } from "@testing-library/react";

const navigate = vi.fn();

vi.mock("react-router-dom", () => ({ useNavigate: () => navigate }));

vi.mock("../../../wailsjs/go/app/App", () => ({
  AnswerToolApproval: vi.fn(),
  AnswerToolPermission: vi.fn(),
  AnswerUserQuestion: vi.fn(),
  OpenPath: vi.fn(),
  ResolveSessionURL: vi.fn(),
  ResolveExecApproval: vi.fn(),
  ResolvePlanAction: vi.fn(),
  WorkspaceFsReadFile: vi.fn(),
}));

vi.mock("../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: vi.fn(),
}));

import { ResolveSessionURL } from "../../../wailsjs/go/app/App";
import { BrowserOpenURL } from "../../../wailsjs/runtime/runtime";

import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useDesktopTranscriptPorts } from "./transcript-ports-desktop";

beforeEach(() => {
  navigate.mockReset();
  vi.mocked(ResolveSessionURL).mockReset();
  vi.mocked(BrowserOpenURL).mockReset();
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

describe("useDesktopTranscriptPorts 的 openExternalURL", () => {
  it("Given a remote session's localhost link, When opened with the session id, Then the browser gets the forwarded address, not the raw link", async () => {
    vi.mocked(ResolveSessionURL).mockResolvedValue("http://127.0.0.1:54321/p?q=1");
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    await result.current.openExternalURL?.("http://localhost:3000/p?q=1", { sessionId: 42 });

    expect(ResolveSessionURL).toHaveBeenCalledWith(42, "http://localhost:3000/p?q=1");
    expect(BrowserOpenURL).toHaveBeenCalledTimes(1);
    expect(BrowserOpenURL).toHaveBeenCalledWith("http://127.0.0.1:54321/p?q=1");
  });

  it("Given a link without a session, When opened, Then it is opened unchanged and nothing is resolved", async () => {
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    await result.current.openExternalURL?.("https://example.com/a");

    expect(ResolveSessionURL).not.toHaveBeenCalled();
    expect(BrowserOpenURL).toHaveBeenCalledWith("https://example.com/a");
  });

  it("Given resolving fails (device offline), When opened, Then the error surfaces and the raw localhost link is not opened", async () => {
    vi.mocked(ResolveSessionURL).mockRejectedValue(new Error("offline"));
    const { result } = renderHook(() => useDesktopTranscriptPorts());

    await expect(
      result.current.openExternalURL?.("http://localhost:3000/", { sessionId: 42 }),
    ).rejects.toThrow("offline");
    expect(BrowserOpenURL).not.toHaveBeenCalled();
  });
});
