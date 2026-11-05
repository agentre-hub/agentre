import { describe, expect, it, vi, type Mock } from "vitest";
import { DEFAULT_NOTIFICATION_SETTINGS } from "../../stores/notification-settings-store";
import type { DoneEvent } from "../../stores/session-status-store";
import {
  classifyTransition,
  maybeNotify,
  type NotifyDeps,
} from "../turn-notify";

// doneWithText 构造一个带 agent 文本块的 done 事件（仅填正文摘取用到的最小字段）。
function doneWithText(...texts: string[]): DoneEvent {
  return {
    kind: "done",
    message: { blocks: texts.map((text) => ({ type: "text", text })) },
  } as unknown as DoneEvent;
}

describe("classifyTransition", () => {
  it("running→idle = done", () => {
    expect(classifyTransition("running", "idle", "done")).toBe("done");
  });
  it("running→idle 但 aborted = null(用户自己停的)", () => {
    expect(classifyTransition("running", "idle", "aborted")).toBeNull();
  });
  it("running→error = error", () => {
    expect(classifyTransition("running", "error", "error")).toBe("error");
  });
  it("running→waiting = waiting", () => {
    expect(classifyTransition("running", "waiting", undefined)).toBe("waiting");
  });
  it("非 running 起点不触发", () => {
    expect(classifyTransition("idle", "running", undefined)).toBeNull();
    expect(classifyTransition(undefined, "running", undefined)).toBeNull();
  });
});

function deps(over: Partial<NotifyDeps> = {}): NotifyDeps {
  return {
    isWindowFocused: () => false,
    getActiveSessionId: () => 0,
    getSettings: () => ({
      ...DEFAULT_NOTIFICATION_SETTINGS,
      toast: true,
    }),
    getSessionTitle: () => "我的会话",
    getDoneEvent: () => null,
    showSystemNotification: vi.fn(),
    showToast: vi.fn(),
    t: ((k: string) => k) as NotifyDeps["t"],
    uiT: ((k: string) => `ui:${k}`) as NotifyDeps["uiT"],
    ...over,
  };
}

describe("maybeNotify", () => {
  it("默认(仅失焦)+ 失焦 → 触发全部已启用渠道", () => {
    const d = deps();
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.done",
    );
    expect(d.showToast).toHaveBeenCalledWith(
      42,
      "done",
      "我的会话",
      "notify.body.done",
    );
  });

  it("默认(仅失焦)+ 聚焦(任意会话)→ 全部静默", () => {
    const d = deps({
      isWindowFocused: () => true,
      getActiveSessionId: () => 7,
    });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).not.toHaveBeenCalled();
    expect(d.showToast).not.toHaveBeenCalled();
  });

  it("关掉 onlyWhenUnfocused + 聚焦 + 非当前会话 → 触发", () => {
    const d = deps({
      isWindowFocused: () => true,
      getActiveSessionId: () => 7,
      getSettings: () => ({
        ...DEFAULT_NOTIFICATION_SETTINGS,
        onlyWhenUnfocused: false,
        toast: true,
      }),
    });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).toHaveBeenCalled();
  });

  it("关掉 onlyWhenUnfocused + 聚焦 + 当前会话 → 静默", () => {
    const d = deps({
      isWindowFocused: () => true,
      getActiveSessionId: () => 42,
      getSettings: () => ({
        ...DEFAULT_NOTIFICATION_SETTINGS,
        onlyWhenUnfocused: false,
        toast: true,
      }),
    });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).not.toHaveBeenCalled();
  });

  it("总开关关 → 不触发", () => {
    const d = deps({
      getSettings: () => ({
        ...DEFAULT_NOTIFICATION_SETTINGS,
        enabled: false,
      }),
    });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).not.toHaveBeenCalled();
  });

  it("无 session 标题时回落 notify.app", () => {
    const d = deps({ getSessionTitle: () => undefined });
    maybeNotify(42, "error", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "notify.app",
      "notify.body.error",
    );
  });

  it("只开系统通知时不弹 toast", () => {
    const d = deps({
      getSettings: () => ({
        ...DEFAULT_NOTIFICATION_SETTINGS,
        toast: false,
        system: true,
      }),
    });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).toHaveBeenCalled();
    expect(d.showToast).not.toHaveBeenCalled();
  });
});

describe("maybeNotify 正文带回复摘要", () => {
  it("done 带 assistant 文本 → 正文 = 状态词 · 摘要(系统通知+toast 共用)", () => {
    const d = deps({
      getDoneEvent: () => doneWithText("已修复登录 bug 并加了测试"),
    });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.done · 已修复登录 bug 并加了测试",
    );
    expect(d.showToast).toHaveBeenCalledWith(
      42,
      "done",
      "我的会话",
      "notify.body.done · 已修复登录 bug 并加了测试",
    );
  });

  it("done 但无文本块 → 退回纯状态词", () => {
    const ev = {
      kind: "done",
      message: { blocks: [{ type: "tool_use" }] },
    } as unknown as DoneEvent;
    const d = deps({ getDoneEvent: () => ev });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.done",
    );
  });

  it("没有 done 事件 → 退回纯状态词", () => {
    const d = deps({ getDoneEvent: () => null });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.done",
    );
  });

  it("取最后一个非空文本块作摘要", () => {
    const d = deps({
      getDoneEvent: () => doneWithText("开始干活", "全部完成"),
    });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.done · 全部完成",
    );
  });

  it("多行/多空白压成单行", () => {
    const d = deps({
      getDoneEvent: () => doneWithText("第一行\n\n  第二行   尾"),
    });
    maybeNotify(42, "done", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.done · 第一行 第二行 尾",
    );
  });

  it("超长摘要截断加省略号", () => {
    const d = deps({ getDoneEvent: () => doneWithText("x".repeat(200)) });
    maybeNotify(42, "done", d);
    const body = (d.showSystemNotification as Mock).mock.calls[0][2] as string;
    const prefix = "notify.body.done · ";
    expect(body.startsWith(prefix)).toBe(true);
    const snippet = body.slice(prefix.length);
    expect(snippet.length).toBe(120);
    expect(snippet.endsWith("…")).toBe(true);
  });

  it("error 带错误文案 → 状态词 · 分类事实句,不贴原文", () => {
    const ev = {
      kind: "error",
      error: "API Error: 529 overloaded_error",
    } as unknown as DoneEvent;
    const d = deps({ getDoneEvent: () => ev });
    maybeNotify(42, "error", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.error · ui:chat.errorCard.fact.overloaded",
    );
  });

  it("error 无错误文案 → 只有状态词", () => {
    const d = deps({ getDoneEvent: () => ({ kind: "error" }) as DoneEvent });
    maybeNotify(42, "error", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.error",
    );
  });

  it("waiting 不带摘要(只状态词)", () => {
    const d = deps({ getDoneEvent: () => doneWithText("不应出现") });
    maybeNotify(42, "waiting", d);
    expect(d.showSystemNotification).toHaveBeenCalledWith(
      42,
      "我的会话",
      "notify.body.waiting",
    );
  });
});
