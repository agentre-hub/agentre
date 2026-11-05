import { act, render } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const showNotification = vi.fn((_req: unknown) => Promise.resolve());
// 仅 toast 命中 → "true",其余 reject(走默认值)。让 load() 产出
// enabled/onlyWhenUnfocused/system 默认开 + toast 开,且不依赖时序。
vi.mock("../../../../wailsjs/go/app/App", () => ({
  ShowNotification: (req: unknown) => showNotification(req),
  GetAppSetting: (req: { key: string }) =>
    req.key === "notify.toast"
      ? Promise.resolve({ key: req.key, value: "true" })
      : Promise.reject(new Error("nf")),
  UpdateAppSettings: vi.fn(() => Promise.resolve({})),
}));
let focused = false;
vi.mock("../../../lib/window-focus", () => ({
  isWindowFocused: () => focused,
}));
const eventHandlers: Record<string, (data: unknown) => void> = {};
vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  EventsOn: (event: string, cb: (data: unknown) => void) => {
    eventHandlers[event] = cb;
    return () => delete eventHandlers[event];
  },
  EventsOff: (event: string) => {
    delete eventHandlers[event];
  },
}));

import { useSessionStatusStore } from "../../../stores/session-status-store";
import { useChatTabsStore } from "../../../stores/chat-tabs-store";
import {
  DEFAULT_NOTIFICATION_SETTINGS,
  useNotificationSettingsStore,
} from "../../../stores/notification-settings-store";
import { useNotificationToastStore } from "../../../stores/notification-toast-store";
import { TurnCompleteNotifier } from "../turn-complete-notifier";

beforeEach(() => {
  vi.clearAllMocks();
  focused = false;
  useSessionStatusStore.getState().__reset();
  useChatTabsStore.setState({ tabs: [], activeTabId: null });
  useNotificationToastStore.getState().clear();
  useNotificationSettingsStore.setState({
    settings: { ...DEFAULT_NOTIFICATION_SETTINGS, toast: true },
  });
});
afterEach(() => vi.restoreAllMocks());

describe("TurnCompleteNotifier", () => {
  it("非当前会话 running→idle 触发系统通知+toast", async () => {
    render(<TurnCompleteNotifier />);
    await act(async () => {}); // 让 load() 完成
    act(() => {
      useSessionStatusStore
        .getState()
        .upsert(42, { agentStatus: "running", needsAttention: false });
    });
    act(() => {
      useSessionStatusStore
        .getState()
        .upsert(42, { agentStatus: "idle", needsAttention: false });
      useSessionStatusStore.getState().bumpDone(42, { kind: "done" });
    });
    expect(showNotification).toHaveBeenCalledTimes(1);
    const toasts = useNotificationToastStore.getState().toasts;
    expect(toasts).toHaveLength(1);
    expect(toasts[0]).toMatchObject({ sessionId: 42, kind: "done" });
  });

  it("挂载前已存在的 idle 会话不误报", async () => {
    act(() => {
      useSessionStatusStore
        .getState()
        .upsert(7, { agentStatus: "idle", needsAttention: false });
    });
    render(<TurnCompleteNotifier />);
    await act(async () => {});
    expect(showNotification).not.toHaveBeenCalled();
  });

  it("收到 notification:click 事件 → 打开/激活对应会话 tab", async () => {
    render(<TurnCompleteNotifier />);
    await act(async () => {});
    act(() => {
      eventHandlers["notification:click"]?.(99);
    });
    const st = useChatTabsStore.getState();
    const tab = st.tabs.find((x) => x.id === st.activeTabId);
    expect(tab?.meta).toMatchObject({ kind: "session", sessionId: 99 });
  });

  it("attention 清除 → 移除该会话通知,其他会话保留", async () => {
    render(<TurnCompleteNotifier />);
    await act(async () => {});
    const up = useSessionStatusStore.getState().upsert;
    act(() => {
      up(42, { agentStatus: "running", needsAttention: false });
      up(43, { agentStatus: "running", needsAttention: false });
    });
    act(() => {
      up(42, { agentStatus: "waiting", needsAttention: true });
      up(43, { agentStatus: "waiting", needsAttention: true });
    });
    expect(useNotificationToastStore.getState().toasts).toHaveLength(2);
    act(() => {
      up(42, { agentStatus: "waiting", needsAttention: false });
    });
    expect(
      useNotificationToastStore.getState().toasts.map((x) => x.sessionId),
    ).toEqual([43]);
  });

  it("新一轮开始(进入 running)→ 移除该会话旧通知", async () => {
    render(<TurnCompleteNotifier />);
    await act(async () => {});
    const up = useSessionStatusStore.getState().upsert;
    act(() => up(42, { agentStatus: "running", needsAttention: false }));
    act(() => up(42, { agentStatus: "error", needsAttention: true }));
    expect(useNotificationToastStore.getState().toasts).toHaveLength(1);
    act(() => up(42, { agentStatus: "running", needsAttention: true }));
    expect(useNotificationToastStore.getState().toasts).toHaveLength(0);
  });
});
