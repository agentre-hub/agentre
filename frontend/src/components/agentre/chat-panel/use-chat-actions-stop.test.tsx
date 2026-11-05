import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

import { useChatActions } from "./use-chat-actions";

const stopChat = vi.hoisted(() => vi.fn());
vi.mock("../../../../wailsjs/go/app/App", async (importOriginal) => ({
  ...(await importOriginal<object>()),
  StopChatMessage: stopChat,
}));

type Props = { sessionId: number; canStop: boolean };

function renderActions(initial: Props) {
  const setNotice = vi.fn();
  const reloadSession = vi.fn(() => Promise.resolve());
  const hook = renderHook(
    ({ sessionId, canStop }: Props) =>
      useChatActions({
        sessionId,
        session: null,
        setMessages: vi.fn(),
        reloadSession,
        openStream: vi.fn(),
        followTranscriptBottom: vi.fn(),
        composerRef: { current: null },
        setSendInFlight: vi.fn(),
        setNotice,
        streaming: canStop,
        canStop,
        activeBackendType: "claudecode",
        canSteer: true,
        isModeSwitchable: false,
        permissionModeValue: "",
        supportsImageInput: true,
        supportsCompactRPC: true,
        execTargetOverride: null,
        effectiveTarget: null,
        providerKey: "",
        modelKey: "",
        reasoningEffort: "",
        recordDraftTarget: vi.fn(),
        editing: false,
        confirmEdit: vi.fn(),
      }),
    { initialProps: initial },
  );
  return { ...hook, setNotice, reloadSession };
}

describe("useChatActions · stopping", () => {
  beforeEach(() => {
    stopChat.mockReset();
  });

  it("Given a running turn, When stop is requested, Then stopping holds until the turn ends and does not leak into the next turn", async () => {
    stopChat.mockResolvedValue({ stopped: true });
    const { result, rerender } = renderActions({
      sessionId: 42,
      canStop: true,
    });
    expect(result.current.stopping).toBe(false);

    await act(async () => {
      await result.current.doStop(42);
    });
    expect(stopChat).toHaveBeenCalledWith({ sessionId: 42 });
    // RPC 回来了但 aborted 事件还没到：这一轮仍在跑，停止键保持 spinner。
    expect(result.current.stopping).toBe(true);

    rerender({ sessionId: 42, canStop: false });
    expect(result.current.stopping).toBe(false);

    rerender({ sessionId: 42, canStop: true });
    expect(result.current.stopping).toBe(false);
  });

  it("Given stop fails, When the error comes back, Then stopping is released so the user can retry", async () => {
    stopChat.mockRejectedValue(new Error("daemon unreachable"));
    const { result, setNotice } = renderActions({
      sessionId: 42,
      canStop: true,
    });

    await act(async () => {
      await result.current.doStop(42);
    });

    expect(result.current.stopping).toBe(false);
    expect(setNotice).toHaveBeenCalledWith(
      expect.objectContaining({ kind: "error" }),
    );
  });

  it("Given session A is stopping, When the panel shows session B, Then B is not shown as stopping", async () => {
    stopChat.mockResolvedValue({ stopped: true });
    const { result, rerender } = renderActions({
      sessionId: 42,
      canStop: true,
    });

    await act(async () => {
      await result.current.doStop(42);
    });
    rerender({ sessionId: 43, canStop: true });

    expect(result.current.stopping).toBe(false);
  });
});
