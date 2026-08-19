import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("react-i18next", () => ({
  useTranslation: () => ({ t: (k: string) => k }),
}));

import { NotificationToastViewport } from "../notification-toast";
import { useChatTabsStore } from "../../../stores/chat-tabs-store";
import { useNotificationToastStore } from "../../../stores/notification-toast-store";
import {
  INITIAL_UPDATE_STATE,
  useUpdateStore,
} from "../../../stores/update-store";

beforeEach(() => {
  vi.clearAllMocks();
  useNotificationToastStore.getState().clear();
});
afterEach(() => {
  vi.useRealTimers();
});

describe("NotificationToastViewport", () => {
  it("渲染推入的 toast(会话名 + 状态文案)", () => {
    render(<NotificationToastViewport />);
    act(() => {
      useNotificationToastStore.getState().push({
        sessionId: 42,
        kind: "done",
        title: "重构 chat_svc",
        body: "会话已完成",
      });
    });
    expect(screen.getByText("重构 chat_svc")).toBeInTheDocument();
    expect(screen.getByText("会话已完成")).toBeInTheDocument();
  });

  it("点「跳转到会话」→ openSession(sessionId) 且移除该条", async () => {
    const openSession = vi.fn();
    useChatTabsStore.setState({ openSession });
    render(<NotificationToastViewport />);
    act(() => {
      useNotificationToastStore
        .getState()
        .push({ sessionId: 42, kind: "done", title: "T", body: "B" });
    });
    await userEvent.click(
      screen.getByRole("button", { name: "notify.openSession" }),
    );
    expect(openSession).toHaveBeenCalledWith(42);
    expect(useNotificationToastStore.getState().toasts).toHaveLength(0);
  });

  it("点关闭 → 移除该条", async () => {
    render(<NotificationToastViewport />);
    act(() => {
      useNotificationToastStore
        .getState()
        .push({ sessionId: 1, kind: "error", title: "T", body: "B" });
    });
    await userEvent.click(
      screen.getByRole("button", { name: "notify.dismiss" }),
    );
    expect(useNotificationToastStore.getState().toasts).toHaveLength(0);
  });

  it("done 到时自动消失", () => {
    vi.useFakeTimers();
    render(<NotificationToastViewport />);
    act(() => {
      useNotificationToastStore
        .getState()
        .push({ sessionId: 1, kind: "done", title: "T", body: "B" });
    });
    expect(useNotificationToastStore.getState().toasts).toHaveLength(1);
    act(() => {
      vi.advanceTimersByTime(6000);
    });
    expect(useNotificationToastStore.getState().toasts).toHaveLength(0);
  });

  it("waiting / error 不自动消失(需用户处理)", () => {
    vi.useFakeTimers();
    render(<NotificationToastViewport />);
    act(() => {
      useNotificationToastStore
        .getState()
        .push({ sessionId: 1, kind: "waiting", title: "T", body: "B" });
    });
    act(() => {
      vi.advanceTimersByTime(30000);
    });
    expect(useNotificationToastStore.getState().toasts).toHaveLength(1);
  });
});

describe("NotificationToastViewport 的新版本到达提示", () => {
  const INFO = {
    hasUpdate: true,
    currentVersion: "0.9.1",
    latestVersion: "v0.9.2",
    releaseNotes: "",
    releaseURL: "",
    publishedAt: "",
  };

  beforeEach(() => {
    useUpdateStore.setState({ ...INITIAL_UPDATE_STATE });
  });

  it("后台检查发现新版本时弹一张卡,与会话 toast 共存于同一视口", () => {
    render(<NotificationToastViewport />);
    act(() => {
      useNotificationToastStore.getState().push({
        sessionId: 7,
        kind: "done",
        title: "重构 chat_svc",
        body: "会话已完成",
      });
      useUpdateStore.setState({
        phase: { kind: "available", info: INFO },
        lastTrigger: "tick",
      });
    });

    expect(screen.getByText("update.toast.title")).toBeInTheDocument();
    expect(screen.getByText("重构 chat_svc")).toBeInTheDocument();
  });

  it("用户主动检查发现的新版本不弹 —— 他正看着结果", () => {
    render(<NotificationToastViewport />);
    act(() => {
      useUpdateStore.setState({
        phase: { kind: "available", info: INFO },
        lastTrigger: "manual",
      });
    });

    expect(screen.queryByText("update.toast.title")).not.toBeInTheDocument();
  });

  it("已跳过的版本不弹", () => {
    render(<NotificationToastViewport />);
    act(() => {
      useUpdateStore.setState({
        phase: { kind: "available", info: INFO },
        lastTrigger: "tick",
        skippedVersion: "v0.9.2",
      });
    });

    expect(screen.queryByText("update.toast.title")).not.toBeInTheDocument();
  });

  it("关掉之后同一版本不再弹", async () => {
    const user = userEvent.setup();
    render(<NotificationToastViewport />);
    act(() => {
      useUpdateStore.setState({
        phase: { kind: "available", info: INFO },
        lastTrigger: "tick",
      });
    });

    await user.click(screen.getByLabelText("update.toast.dismiss"));
    expect(screen.queryByText("update.toast.title")).not.toBeInTheDocument();

    // 又一次后台检查报同一个版本:不该再弹。
    act(() => {
      useUpdateStore.setState({
        phase: { kind: "available", info: INFO },
        lastTrigger: "tick",
      });
    });
    expect(screen.queryByText("update.toast.title")).not.toBeInTheDocument();
  });

  it("出现更新的版本时重新计一次", async () => {
    const user = userEvent.setup();
    render(<NotificationToastViewport />);
    act(() => {
      useUpdateStore.setState({
        phase: { kind: "available", info: INFO },
        lastTrigger: "tick",
      });
    });
    await user.click(screen.getByLabelText("update.toast.dismiss"));

    act(() => {
      useUpdateStore.setState({
        phase: {
          kind: "available",
          info: { ...INFO, latestVersion: "v0.9.3" },
        },
        lastTrigger: "tick",
      });
    });

    expect(screen.getByText("update.toast.title")).toBeInTheDocument();
  });

  it("点「查看更新」请求打开更新面板,并收起卡片", async () => {
    const user = userEvent.setup();
    render(<NotificationToastViewport />);
    act(() => {
      useUpdateStore.setState({
        phase: { kind: "available", info: INFO },
        lastTrigger: "tick",
      });
    });

    await user.click(screen.getByText("update.toast.action"));

    expect(useUpdateStore.getState().panelOpen).toBe(true);
    expect(screen.queryByText("update.toast.title")).not.toBeInTheDocument();
  });

  it("8 秒后自动消失 —— 胶囊已经兜住「错过」,不必常驻", () => {
    vi.useFakeTimers();
    render(<NotificationToastViewport />);
    act(() => {
      useUpdateStore.setState({
        phase: { kind: "available", info: INFO },
        lastTrigger: "tick",
      });
    });
    expect(screen.getByText("update.toast.title")).toBeInTheDocument();

    act(() => {
      vi.advanceTimersByTime(8000);
    });

    expect(screen.queryByText("update.toast.title")).not.toBeInTheDocument();
  });
});
