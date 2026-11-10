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

const appMocks = vi.hoisted(() => ({
  SetChatSessionReasoningEffort: vi.fn(),
}));
vi.mock("../../../../wailsjs/go/app/App", () => appMocks);

const sonnerMocks = vi.hoisted(() => ({
  toast: Object.assign(vi.fn(), { error: vi.fn(), success: vi.fn() }),
}));
vi.mock("sonner", () => sonnerMocks);

import type { ReasoningEffortValue } from "@agentre-hub/agentre-ui";

import { useReasoningEffortPill } from "./use-reasoning-effort-pill";

function setup(
  persisted: ReasoningEffortValue,
  backend: ReasoningEffortValue = "",
) {
  const onSwitched = vi.fn();
  const hook = renderHook(() =>
    useReasoningEffortPill({
      sessionId: 42,
      persistedReasoningEffort: persisted,
      persistedBackendReasoningEffort: backend,
      onSwitched,
    }),
  );
  return { hook, onSwitched };
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
describe("useReasoningEffortPill · switch failure", () => {
  it("Given the write is rejected, When a level is chosen, Then the value rolls back and a toast names the level still in effect with the raw text only in a collapsed Details", async () => {
    appMocks.SetChatSessionReasoningEffort.mockRejectedValue(
      new Error("rpc error: db locked"),
    );
    const { hook, onSwitched } = setup("medium");

    act(() => hook.result.current.onChange("high"));

    await waitFor(() => expect(hook.result.current.value).toBe("medium"));
    const { title, options } = lastErrorToast();
    expect(title).toBe("Reasoning effort didn't switch — still medium");
    expect(hook.result.current.error).toBe("The last switch didn't go through");
    expect(onSwitched).not.toHaveBeenCalled();

    render(<>{options.description as React.ReactNode}</>);
    const details = screen.getByText(/db locked/).closest("details");
    expect(details).not.toBeNull();
    expect(details).not.toHaveAttribute("open");
  });

  it("Given the session follows the backend config, When the switch fails, Then the toast names the effective backend level, or Default when none is set", async () => {
    appMocks.SetChatSessionReasoningEffort.mockRejectedValue(new Error("x"));
    const withBackend = setup("", "low");
    act(() => withBackend.hook.result.current.onChange("max"));
    await waitFor(() => expect(sonnerMocks.toast.error).toHaveBeenCalled());
    expect(lastErrorToast().title).toBe(
      "Reasoning effort didn't switch — still low",
    );

    const unset = setup("", "");
    act(() => unset.hook.result.current.onChange("max"));
    await waitFor(() =>
      expect(sonnerMocks.toast.error).toHaveBeenCalledTimes(2),
    );
    expect(lastErrorToast().title).toBe(
      "Reasoning effort didn't switch — still Default",
    );
  });

  it("Given the write succeeds, When a level is chosen, Then no toast is shown", async () => {
    appMocks.SetChatSessionReasoningEffort.mockResolvedValue(undefined);
    const { hook, onSwitched } = setup("medium");

    act(() => hook.result.current.onChange("high"));

    await waitFor(() => expect(onSwitched).toHaveBeenCalled());
    expect(hook.result.current.value).toBe("high");
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });
});
