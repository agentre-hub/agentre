import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";

const handlers = new Map<string, Set<(payload: unknown) => void>>();
const offCalls: string[] = [];

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: (name: string, cb: (payload: unknown) => void) => {
    const set = handlers.get(name) ?? new Set<(payload: unknown) => void>();
    set.add(cb);
    handlers.set(name, set);
    return () => {
      set.delete(cb);
      offCalls.push(name);
    };
  },
}));

import { useRefreshSignal } from "../use-refresh-signal";

function emit(name: string) {
  for (const cb of [...(handlers.get(name) ?? [])]) cb(undefined);
}

describe("useRefreshSignal", () => {
  it("Given the hook is mounted, When config:changed arrives, Then the returned signal changes", () => {
    handlers.clear();
    const { result } = renderHook(() => useRefreshSignal());
    const before = result.current;

    act(() => emit("config:changed"));

    expect(result.current).not.toBe(before);
  });

  it("Given the hook is mounted, When sync:applied arrives, Then the returned signal changes", () => {
    handlers.clear();
    const { result } = renderHook(() => useRefreshSignal());
    const before = result.current;

    act(() => emit("sync:applied"));

    expect(result.current).not.toBe(before);
  });

  it("Given the hook unmounts, When it goes away, Then it takes both subscriptions with it", () => {
    handlers.clear();
    offCalls.length = 0;
    const { unmount } = renderHook(() => useRefreshSignal());

    unmount();

    expect(offCalls).toContain("config:changed");
    expect(offCalls).toContain("sync:applied");
  });
});
