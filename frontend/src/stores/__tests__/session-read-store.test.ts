import { beforeEach, describe, expect, it } from "vitest";

import { useSessionReadStore } from "../session-read-store";

describe("useSessionReadStore", () => {
  beforeEach(() => {
    useSessionReadStore.setState({ overrides: new Map() });
  });

  it("markRead stores override and is single-source mutable across getState()", () => {
    useSessionReadStore.getState().markRead(42, 1700);
    expect(useSessionReadStore.getState().overrides.get(42)).toBe(1700);
  });

  it("markRead is monotonic — older ts ignored", () => {
    useSessionReadStore.getState().markRead(42, 2000);
    useSessionReadStore.getState().markRead(42, 1000);
    expect(useSessionReadStore.getState().overrides.get(42)).toBe(2000);
  });

  it("markRead ignores invalid sessionId/ts", () => {
    useSessionReadStore.getState().markRead(0, 1700);
    useSessionReadStore.getState().markRead(42, 0);
    useSessionReadStore.getState().markRead(-1, 1700);
    expect(useSessionReadStore.getState().overrides.size).toBe(0);
  });

  it("markRead is reference-stable when value didn't change (no extra re-renders)", () => {
    useSessionReadStore.getState().markRead(42, 1700);
    const before = useSessionReadStore.getState().overrides;
    useSessionReadStore.getState().markRead(42, 500);
    expect(useSessionReadStore.getState().overrides).toBe(before);
  });
});
