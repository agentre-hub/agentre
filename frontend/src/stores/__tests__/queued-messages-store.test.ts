import { beforeEach, describe, expect, it } from "vitest";

import { useQueuedMessagesStore } from "../queued-messages-store";

describe("queued-messages-store", () => {
  beforeEach(() => useQueuedMessagesStore.getState().__reset());

  it("append / consume all / clear", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "hello", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "b", text: "world", cancellable: false });
    expect(
      useQueuedMessagesStore.getState().queuedBySession.get(1)?.items.length,
    ).toBe(2);

    const consumed = useQueuedMessagesStore.getState().consume(1);
    expect(consumed.map((m) => m.id)).toEqual(["a", "b"]);
    expect(useQueuedMessagesStore.getState().queuedBySession.has(1)).toBe(
      false,
    );
  });

  it("consume with ids removes matching entries", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "1", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "b", text: "2", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "c", text: "3", cancellable: false });

    const removed = useQueuedMessagesStore.getState().consume(1, ["a", "c"]);
    expect(removed.map((m) => m.id)).toEqual(["a", "c"]);
    const remaining = useQueuedMessagesStore.getState().queuedBySession.get(1);
    expect(remaining?.items.map((m) => m.id)).toEqual(["b"]);
  });

  it("clear removes all entries for session", () => {
    useQueuedMessagesStore
      .getState()
      .append(2, { id: "x", text: "x", cancellable: true });
    useQueuedMessagesStore.getState().clear(2);
    expect(useQueuedMessagesStore.getState().queuedBySession.has(2)).toBe(
      false,
    );
  });

  it("per-session isolation", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "1", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(2, { id: "b", text: "2", cancellable: true });
    useQueuedMessagesStore.getState().clear(1);
    expect(useQueuedMessagesStore.getState().queuedBySession.has(1)).toBe(
      false,
    );
    expect(
      useQueuedMessagesStore.getState().queuedBySession.get(2)?.items.length,
    ).toBe(1);
  });

  it("clear on non-existent session is a no-op (referential stability)", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "1", cancellable: true });
    const before = useQueuedMessagesStore.getState().queuedBySession;
    useQueuedMessagesStore.getState().clear(999);
    expect(useQueuedMessagesStore.getState().queuedBySession).toBe(before);
  });

  it("markDropped moves a non-empty queue into dropped and clears it", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "hello", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "b", text: "world", cancellable: false });

    useQueuedMessagesStore.getState().markDropped(1);

    expect(useQueuedMessagesStore.getState().queuedBySession.has(1)).toBe(
      false,
    );
    const dropped = useQueuedMessagesStore.getState().dropped;
    expect(dropped?.sessionId).toBe(1);
    expect(dropped?.items.map((m) => m.id)).toEqual(["a", "b"]);
    expect(typeof dropped?.at).toBe("number");
  });

  it("markDropped on an empty queue is a no-op (nothing to drop)", () => {
    const before = useQueuedMessagesStore.getState().queuedBySession;
    useQueuedMessagesStore.getState().markDropped(1);
    expect(useQueuedMessagesStore.getState().queuedBySession).toBe(before);
    expect(useQueuedMessagesStore.getState().dropped).toBeNull();
  });

  it("dismissDropped clears the dropped record", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "hello", cancellable: true });
    useQueuedMessagesStore.getState().markDropped(1);
    expect(useQueuedMessagesStore.getState().dropped).not.toBeNull();

    useQueuedMessagesStore.getState().dismissDropped();

    expect(useQueuedMessagesStore.getState().dropped).toBeNull();
    expect(useQueuedMessagesStore.getState().queuedBySession.has(1)).toBe(
      false,
    );
  });

  it("Given dropped items, When the draft is taken, Then their texts come back in order joined by a blank line and nothing is re-queued", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "first", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "b", text: "second", cancellable: true });
    useQueuedMessagesStore.getState().markDropped(1);
    // 其它会话的队列不受影响
    useQueuedMessagesStore
      .getState()
      .append(2, { id: "z", text: "other", cancellable: true });

    const draft = useQueuedMessagesStore.getState().takeDroppedDraft();

    expect(draft).toBe("first\n\nsecond");
    expect(useQueuedMessagesStore.getState().dropped).toBeNull();
    expect(useQueuedMessagesStore.getState().queuedBySession.has(1)).toBe(
      false,
    );
    expect(
      useQueuedMessagesStore
        .getState()
        .queuedBySession.get(2)
        ?.items.map((m) => m.id),
    ).toEqual(["z"]);
  });

  it("Given a queued item, When its move-back starts and is refused, Then the withdrawing mark is set and then cleared on that item only", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "first", cancellable: true });
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "b", text: "second", cancellable: true });

    useQueuedMessagesStore.getState().setWithdrawing(1, "a", true);
    expect(
      useQueuedMessagesStore
        .getState()
        .queuedBySession.get(1)
        ?.items.map((m) => [m.id, m.withdrawing ?? false]),
    ).toEqual([
      ["a", true],
      ["b", false],
    ]);

    useQueuedMessagesStore.getState().setWithdrawing(1, "a", false);
    expect(
      useQueuedMessagesStore.getState().queuedBySession.get(1)?.items[0]
        ?.withdrawing,
    ).toBeFalsy();
  });

  it("takeDroppedDraft / dismissDropped with nothing dropped are referential no-ops", () => {
    const before = useQueuedMessagesStore.getState().queuedBySession;
    expect(useQueuedMessagesStore.getState().takeDroppedDraft()).toBe("");
    useQueuedMessagesStore.getState().dismissDropped();
    expect(useQueuedMessagesStore.getState().queuedBySession).toBe(before);
  });

  it("__reset clears dropped too", () => {
    useQueuedMessagesStore
      .getState()
      .append(1, { id: "a", text: "x", cancellable: true });
    useQueuedMessagesStore.getState().markDropped(1);
    useQueuedMessagesStore.getState().__reset();
    expect(useQueuedMessagesStore.getState().dropped).toBeNull();
  });
});
