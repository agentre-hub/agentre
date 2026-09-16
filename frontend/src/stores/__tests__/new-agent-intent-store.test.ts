import { beforeEach, describe, expect, it, vi } from "vitest";

import { useNewAgentIntentStore } from "../new-agent-intent-store";

const request = () => useNewAgentIntentStore.getState().request();
const consume = () => useNewAgentIntentStore.getState().consume();
// 与生产订阅同一条口径：只认 request（revision 变了），consume 不叫醒订阅方。
const subscribe = (listener: () => void) =>
  useNewAgentIntentStore.subscribe((state) => state.revision, listener);

describe("new-agent intent store", () => {
  beforeEach(() => {
    consume();
  });

  it("Given no pending intent, when requested and consumed, then it is cleared", () => {
    expect(consume()).toBe(false);

    request();

    expect(consume()).toBe(true);
    expect(consume()).toBe(false);
  });

  it("Given a subscriber, when the intent is requested twice, then each request is observable and consumption stays idempotent", () => {
    const listener = vi.fn();
    const unsubscribe = subscribe(listener);

    request();
    request();

    expect(listener).toHaveBeenCalledTimes(2);
    expect(consume()).toBe(true);
    expect(consume()).toBe(false);

    unsubscribe();
    request();
    expect(listener).toHaveBeenCalledTimes(2);
    expect(consume()).toBe(true);
  });
});
