import {
  act,
  render,
  renderHook,
  screen,
  waitFor,
} from "@testing-library/react";
import type { ExternalToast } from "sonner";
import { beforeEach, describe, expect, it, vi } from "vitest";

import "@/i18n";

const appMocks = vi.hoisted(() => ({ SetChatPermissionMode: vi.fn() }));
vi.mock("@/../wailsjs/go/app/App", () => appMocks);

const sonnerMocks = vi.hoisted(() => ({
  toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }),
}));
vi.mock("sonner", () => sonnerMocks);

import { usePermissionMode } from "./use-permission-mode";

const META = {
  allowedModes: ["default", "acceptEdits", "plan"],
  defaultMode: "default",
  switchableDuringTurn: true,
  order: ["default", "acceptEdits", "plan"],
};

function setup(sessionId = 42) {
  return renderHook(() =>
    usePermissionMode({
      sessionId,
      permissionModeMeta: META,
      runtimeKey: "claudecode",
      initialMode: "acceptEdits",
    }),
  );
}

function lastErrorToast(): { title: unknown; options: ExternalToast } {
  const calls = sonnerMocks.toast.error.mock.calls;
  const [title, options] = calls[calls.length - 1] ?? [];
  return { title, options: (options ?? {}) as ExternalToast };
}

beforeEach(() => {
  vi.clearAllMocks();
});

// A2（spec 2026-11-10「输入框药丸切换失败」）
describe("usePermissionMode · switch failure", () => {
  it("Given the write is rejected, When a mode is chosen, Then the mode rolls back and a toast names the mode still in effect with the raw text only in a collapsed Details", async () => {
    appMocks.SetChatPermissionMode.mockRejectedValue(
      new Error("rpc error: cli gone"),
    );
    const hook = setup();

    act(() => hook.result.current.setMode("plan"));

    await waitFor(() => expect(hook.result.current.mode).toBe("acceptEdits"));
    const { title, options } = lastErrorToast();
    expect(title).toBe("Permission mode didn't switch — still Accept Edits");
    // 弹层底栏只说本地化事实，不贴原话。
    expect(hook.result.current.error).toBe("The last switch didn't go through");

    render(<>{options.description as React.ReactNode}</>);
    const details = screen.getByText(/cli gone/).closest("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
  });

  it("Given an unknown previous mode, When the switch fails, Then the toast falls back to the mode key", async () => {
    appMocks.SetChatPermissionMode.mockRejectedValue(new Error("x"));
    const hook = renderHook(() =>
      usePermissionMode({
        sessionId: 42,
        permissionModeMeta: {
          ...META,
          allowedModes: ["auto", "plan"],
          order: ["auto", "plan"],
          defaultMode: "auto",
        },
        initialMode: "auto",
      }),
    );

    act(() => hook.result.current.setMode("plan"));

    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(lastErrorToast().title).toBe(
      "Permission mode didn't switch — still auto",
    );
  });

  it("Given the write succeeds, When a mode is chosen, Then no toast is shown and the mode sticks", async () => {
    appMocks.SetChatPermissionMode.mockResolvedValue(undefined);
    const hook = setup();

    act(() => hook.result.current.setMode("plan"));

    await waitFor(() =>
      expect(appMocks.SetChatPermissionMode).toHaveBeenCalled(),
    );
    expect(hook.result.current.mode).toBe("plan");
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });
});
