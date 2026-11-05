import { beforeEach, describe, expect, it, vi } from "vitest";

import { useNotificationToastStore } from "../notification-toast-store";

beforeEach(() => useNotificationToastStore.getState().clear());

describe("notification-toast-store", () => {
  it("push 追加一条并返回自增 id", () => {
    const a = useNotificationToastStore
      .getState()
      .push({ sessionId: 1, kind: "done", title: "A", body: "完成" });
    const b = useNotificationToastStore
      .getState()
      .push({ sessionId: 2, kind: "error", title: "B", body: "出错" });
    expect(b).toBeGreaterThan(a);
    const { toasts } = useNotificationToastStore.getState();
    expect(toasts).toHaveLength(2);
    expect(toasts[0]).toMatchObject({
      id: a,
      sessionId: 1,
      kind: "done",
      title: "A",
      body: "完成",
    });
  });

  it("dismiss 按 id 移除", () => {
    const a = useNotificationToastStore
      .getState()
      .push({ sessionId: 1, kind: "done", title: "A", body: "完成" });
    useNotificationToastStore
      .getState()
      .push({ sessionId: 2, kind: "error", title: "B", body: "出错" });
    useNotificationToastStore.getState().dismiss(a);
    const { toasts } = useNotificationToastStore.getState();
    expect(toasts).toHaveLength(1);
    expect(toasts[0].sessionId).toBe(2);
  });

  it("超过上限丢弃最旧", () => {
    for (let i = 0; i < 7; i++) {
      useNotificationToastStore
        .getState()
        .push({ sessionId: i, kind: "done", title: String(i), body: "b" });
    }
    const { toasts } = useNotificationToastStore.getState();
    expect(toasts).toHaveLength(5);
    expect(toasts[0].title).toBe("2");
    expect(toasts[4].title).toBe("6");
  });

  it("push 记录 createdAt(当前时间)", () => {
    vi.useFakeTimers();
    vi.setSystemTime(1_700_000_000_000);
    useNotificationToastStore
      .getState()
      .push({ sessionId: 1, kind: "done", title: "A", body: "x" });
    vi.useRealTimers();
    expect(useNotificationToastStore.getState().toasts[0].createdAt).toBe(
      1_700_000_000_000,
    );
  });

  it("同一会话只留最新一张,其他会话不受影响", () => {
    const s = useNotificationToastStore.getState();
    s.push({ sessionId: 1, kind: "waiting", title: "A", body: "old" });
    s.push({ sessionId: 2, kind: "done", title: "B", body: "b" });
    const latest = s.push({
      sessionId: 1,
      kind: "error",
      title: "A",
      body: "new",
    });
    const { toasts } = useNotificationToastStore.getState();
    expect(toasts.map((x) => x.sessionId)).toEqual([2, 1]);
    expect(toasts[1]).toMatchObject({ id: latest, body: "new" });
  });

  it("dismissBySession 移除该会话的通知,保留其他会话", () => {
    const s = useNotificationToastStore.getState();
    s.push({ sessionId: 1, kind: "waiting", title: "A", body: "a" });
    s.push({ sessionId: 2, kind: "error", title: "B", body: "b" });
    s.dismissBySession(1);
    expect(
      useNotificationToastStore.getState().toasts.map((x) => x.sessionId),
    ).toEqual([2]);
    s.dismissBySession(99);
    expect(useNotificationToastStore.getState().toasts).toHaveLength(1);
  });
});
