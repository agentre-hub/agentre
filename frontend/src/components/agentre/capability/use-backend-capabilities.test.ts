import { renderHook, waitFor } from "@testing-library/react";
import { describe, it, expect, vi, beforeEach } from "vitest";

import { useBackendCapabilities } from "./use-backend-capabilities";

const getBackendCapabilities = vi.fn();

vi.mock("@/../wailsjs/go/app/App", () => ({
  GetBackendCapabilities: (req: { backendType: string }) =>
    getBackendCapabilities(req),
}));

describe("useBackendCapabilities", () => {
  beforeEach(() => {
    getBackendCapabilities.mockReset();
  });

  it("loads caps + permission mode meta for the given backend type", async () => {
    getBackendCapabilities.mockResolvedValue({
      capabilities: ["abort", "set_permission_mode"],
      permissionModeMeta: {
        allowedModes: ["default", "acceptEdits", "plan", "bypassPermissions"],
        defaultMode: "acceptEdits",
        switchableDuringTurn: true,
        order: ["default", "acceptEdits", "plan", "bypassPermissions"],
      },
    });
    const { result } = renderHook(() => useBackendCapabilities("claudecode"));
    await waitFor(() => expect(result.current.caps).not.toBeNull());
    expect(result.current.caps?.has("set_permission_mode")).toBe(true);
    expect(result.current.caps?.permissionModeMeta.defaultMode).toBe(
      "acceptEdits",
    );
    expect(getBackendCapabilities).toHaveBeenCalledWith({
      backendType: "claudecode",
    });
  });

  // 新对话还没有会话行时走的是这条查询,选择器的渲染判定必须在这里也拿得到那一位。
  it("surfaces reasoning_effort for a supporting backend and withholds it for openclaw", async () => {
    getBackendCapabilities.mockResolvedValue({
      capabilities: ["abort", "reasoning_effort"],
    });
    const { result } = renderHook(() => useBackendCapabilities("codex"));
    await waitFor(() => expect(result.current.caps).not.toBeNull());
    expect(result.current.caps?.has("reasoning_effort")).toBe(true);

    getBackendCapabilities.mockResolvedValue({
      capabilities: ["abort", "exec_approval"],
    });
    const { result: openclaw } = renderHook(() =>
      useBackendCapabilities("openclaw"),
    );
    await waitFor(() => expect(openclaw.current.caps).not.toBeNull());
    expect(openclaw.current.caps?.has("reasoning_effort")).toBe(false);
  });

  it("returns null caps when backendType is empty/undefined/null", () => {
    const { result: rE } = renderHook(() => useBackendCapabilities(""));
    expect(rE.current.caps).toBeNull();
    const { result: rU } = renderHook(() => useBackendCapabilities(undefined));
    expect(rU.current.caps).toBeNull();
    const { result: rN } = renderHook(() => useBackendCapabilities(null));
    expect(rN.current.caps).toBeNull();
    expect(getBackendCapabilities).not.toHaveBeenCalled();
  });

  it("re-fetches when backendType changes", async () => {
    getBackendCapabilities.mockResolvedValue({
      capabilities: [],
      permissionModeMeta: {
        allowedModes: [],
        defaultMode: "",
        switchableDuringTurn: false,
        order: [],
      },
    });
    const { rerender } = renderHook(
      ({ bt }: { bt: string }) => useBackendCapabilities(bt),
      { initialProps: { bt: "claudecode" } },
    );
    await waitFor(() =>
      expect(getBackendCapabilities).toHaveBeenCalledWith({
        backendType: "claudecode",
      }),
    );
    rerender({ bt: "codex" });
    await waitFor(() =>
      expect(getBackendCapabilities).toHaveBeenCalledWith({
        backendType: "codex",
      }),
    );
  });

  it("sets caps to null on backend error", async () => {
    const errSpy = vi.spyOn(console, "error").mockImplementation(() => {});
    getBackendCapabilities.mockRejectedValue(new Error("unknown backend"));
    const { result } = renderHook(() => useBackendCapabilities("claudecode"));
    await waitFor(() => expect(errSpy).toHaveBeenCalled());
    expect(result.current.caps).toBeNull();
    errSpy.mockRestore();
  });
});
