import { StrictMode } from "react";
import { act, renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

// 这套桩照抄真实 wails runtime 的两条语义，别把它简化成空 no-op：
// EventsOn 返回「只摘掉这一个监听器」的取消函数，EventsOff 按事件名一次清掉
// 该名下**全部**监听器。差别正是重复挂载会不会把订阅清空的分界线。
const runtimeMocks = vi.hoisted(() => {
  const listeners = new Map<string, Set<(payload: never) => void>>();
  return {
    listeners,
    EventsOn: vi.fn((name: string, cb: (payload: never) => void) => {
      let set = listeners.get(name);
      if (!set) {
        set = new Set();
        listeners.set(name, set);
      }
      set.add(cb);
      return () => set?.delete(cb);
    }),
    EventsOff: vi.fn((...names: string[]) => {
      for (const name of names) listeners.delete(name);
    }),
  };
});

function liveListeners(name: string): number {
  return runtimeMocks.listeners.get(name)?.size ?? 0;
}

vi.mock("../../../wailsjs/runtime/runtime", () => runtimeMocks);

import {
  INITIAL_UPDATE_STATE,
  pendingAnnouncement,
  unskippedUpdate,
  useUpdateStore,
  useUpdateWatch,
  type UpdateCheckOutcome,
} from "../update-store";

const INFO = {
  hasUpdate: true,
  currentVersion: "0.9.1",
  latestVersion: "v0.9.2",
  releaseNotes: "修了一堆",
  releaseURL: "https://example/releases/v0.9.2",
  publishedAt: "2026-08-17T00:00:00Z",
};

const UP_TO_DATE = { ...INFO, hasUpdate: false, latestVersion: "0.9.1" };

type AppMock = Record<string, ReturnType<typeof vi.fn>>;

function installBindings(overrides?: AppMock): AppMock {
  const app: AppMock = {
    CheckForUpdate: vi.fn(() => Promise.resolve(INFO)),
    MaybeCheckForUpdate: vi.fn(() => Promise.resolve(INFO)),
    DownloadAndInstallUpdate: vi.fn(() => Promise.resolve()),
    RestartApp: vi.fn(() => Promise.resolve()),
    GetAppSetting: vi.fn(() => Promise.reject(new Error("AppSettingNotFound"))),
    UpdateAppSettings: vi.fn(() => Promise.resolve({})),
    ...overrides,
  };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { app: { App: app } },
  });
  return app;
}

// emit 把一条事件投给当下**还活着**的监听器 —— 而不是 EventsOn 的调用记录：
// 后者连已经解绑的那一份也会被翻出来，测不出订阅有没有被清掉。
function emit(name: string, payload: unknown) {
  const set = runtimeMocks.listeners.get(name);
  if (!set || set.size === 0) throw new Error(`${name} 未被订阅`);
  for (const cb of [...set]) (cb as (p: unknown) => void)(payload);
}

function emitChecked(outcome: UpdateCheckOutcome) {
  emit("update:checked", outcome);
}

function emitProgress(downloaded: number, total: number) {
  emit("update:progress", { downloaded, total });
}

beforeEach(() => {
  vi.clearAllMocks();
  runtimeMocks.listeners.clear();
  useUpdateStore.setState({ ...INITIAL_UPDATE_STATE });
  installBindings();
});

describe("update-store 后台检查结果的接收", () => {
  it("Given 后台检查发现新版本, When update:checked 到达, Then 进入 available 并记下触发源", async () => {
    await useUpdateStore.getState().init();

    emitChecked({ trigger: "tick", info: INFO, error: "" });

    const s = useUpdateStore.getState();
    expect(s.phase).toEqual({ kind: "available", info: INFO });
    expect(s.lastTrigger).toBe("tick");
  });

  it("Given 后台检查未发现新版本, When 结果到达, Then 进入 uptodate 而不是 idle", async () => {
    await useUpdateStore.getState().init();

    emitChecked({ trigger: "startup", info: UP_TO_DATE, error: "" });

    expect(useUpdateStore.getState().phase).toEqual({ kind: "uptodate" });
  });

  it("Given 后台检查失败, When 结果到达, Then 进入 error 并带上原始错误文本", async () => {
    await useUpdateStore.getState().init();

    emitChecked({
      trigger: "tick",
      info: null,
      error: 'Get "https://api.github.com/...": i/o timeout',
    });

    expect(useUpdateStore.getState().phase).toEqual({
      kind: "error",
      message: 'Get "https://api.github.com/...": i/o timeout',
    });
  });

  it("Given 用户正在下载, When 后台 tick 的结果到达, Then 不打断下载进度", async () => {
    await useUpdateStore.getState().init();
    useUpdateStore.setState({
      phase: { kind: "downloading", info: INFO, progress: 42 },
    });

    emitChecked({ trigger: "tick", info: INFO, error: "" });

    expect(useUpdateStore.getState().phase).toEqual({
      kind: "downloading",
      info: INFO,
      progress: 42,
    });
  });

  it("Given 已装好待重启, When 后台 tick 的结果到达, Then 仍停在 installed", async () => {
    await useUpdateStore.getState().init();
    useUpdateStore.setState({ phase: { kind: "installed", info: INFO } });

    emitChecked({ trigger: "tick", info: UP_TO_DATE, error: "" });

    expect(useUpdateStore.getState().phase).toEqual({
      kind: "installed",
      info: INFO,
    });
  });

  it("Given init 已挂载, When 卸载, Then 两个事件都被解绑", async () => {
    const dispose = await useUpdateStore.getState().init();

    dispose();

    expect(liveListeners("update:checked")).toBe(0);
    expect(liveListeners("update:progress")).toBe(0);
  });
});

describe("update-store 主动检查", () => {
  it("Given 用户点检查更新, When 有新版本, Then 走绕过节流的绑定并记 manual", async () => {
    const app = installBindings();

    await useUpdateStore.getState().check("manual");

    expect(app.CheckForUpdate).toHaveBeenCalled();
    expect(app.MaybeCheckForUpdate).not.toHaveBeenCalled();
    expect(useUpdateStore.getState().phase).toEqual({
      kind: "available",
      info: INFO,
    });
    expect(useUpdateStore.getState().lastTrigger).toBe("manual");
  });

  it("Given 窗口重新获得焦点, When 触发检查, Then 走受节流的绑定", async () => {
    const app = installBindings();

    await useUpdateStore.getState().check("focus");

    expect(app.MaybeCheckForUpdate).toHaveBeenCalled();
    expect(app.CheckForUpdate).not.toHaveBeenCalled();
  });

  it("Given 受节流的检查被跳过（返回 null）, When 结果回来, Then 保持原状态不当成已是最新", async () => {
    installBindings({
      MaybeCheckForUpdate: vi.fn(() => Promise.resolve(null)),
    });
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    await useUpdateStore.getState().check("focus");

    expect(useUpdateStore.getState().phase).toEqual({
      kind: "available",
      info: INFO,
    });
  });

  it("Given 检查抛错, When 结果回来, Then 进入 error", async () => {
    installBindings({
      CheckForUpdate: vi.fn(() => Promise.reject(new Error("网络不通"))),
    });

    await useUpdateStore.getState().check("manual");

    expect(useUpdateStore.getState().phase).toEqual({
      kind: "error",
      message: "网络不通",
    });
  });

  it("Given 用户自己查出了新版本, When 回窗补检被节流跳过, Then 不改判成「后台发现的」", async () => {
    // 什么都没查的一次补检不产生「最近一次结果」。若它照样把触发源改写成 focus,
    // 用户刚在设置页亲手查出来的更新会因为切了一下窗口就弹一张到达提示。
    installBindings({
      MaybeCheckForUpdate: vi.fn(() => Promise.resolve(null)),
    });
    await useUpdateStore.getState().check("manual");
    expect(pendingAnnouncement(useUpdateStore.getState())).toBeNull();

    await useUpdateStore.getState().check("focus");

    expect(useUpdateStore.getState().lastTrigger).toBe("manual");
    expect(pendingAnnouncement(useUpdateStore.getState())).toBeNull();
  });

  it("Given 正在检查, When 再次触发, Then 不重复发起", async () => {
    const app = installBindings();
    useUpdateStore.setState({ phase: { kind: "checking" } });

    await useUpdateStore.getState().check("focus");

    expect(app.MaybeCheckForUpdate).not.toHaveBeenCalled();
  });
});

describe("update-store 下载与进度", () => {
  it("Given 有新版本, When 开始下载, Then 进度事件推进百分比并带上已下载/总量", async () => {
    await useUpdateStore.getState().init();
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    // download() 同步跑到第一个 await 就挂起，此时可以确定地投一条进度事件进去。
    const pending = useUpdateStore.getState().download(false);
    emitProgress(50, 200);

    // 字节数一起留在阶段里：面板要说「12.0 MB / 48.0 MB」，百分比说不出还要等多久。
    expect(useUpdateStore.getState().phase).toEqual({
      kind: "downloading",
      info: INFO,
      progress: 25,
      downloaded: 50,
      total: 200,
    });

    await pending;

    expect(useUpdateStore.getState().phase).toEqual({
      kind: "installed",
      info: INFO,
    });
  });

  it("Given 校验文件拉不到, When 下载报 CHECKSUM_FETCH_FAILED, Then 回到 available 并弹确认", async () => {
    installBindings({
      DownloadAndInstallUpdate: vi.fn(() =>
        Promise.reject(new Error("CHECKSUM_FETCH_FAILED:404 not found")),
      ),
    });
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    await useUpdateStore.getState().download(false);

    const s = useUpdateStore.getState();
    expect(s.phase).toEqual({ kind: "available", info: INFO });
    expect(s.checksumPrompt).toEqual({ open: true, reason: "404 not found" });
  });

  it("Given 下载失败, When 不是校验错误, Then 进入 error", async () => {
    installBindings({
      DownloadAndInstallUpdate: vi.fn(() =>
        Promise.reject(new Error("磁盘满")),
      ),
    });
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    await useUpdateStore.getState().download(false);

    expect(useUpdateStore.getState().phase).toEqual({
      kind: "error",
      message: "磁盘满",
    });
  });
});

describe("update-store 跳过版本", () => {
  it("Given 有新版本, When 跳过, Then 持久化版本号且状态栏陈述不变", async () => {
    const app = installBindings();
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    await useUpdateStore.getState().skipCurrentVersion();

    expect(app.UpdateAppSettings).toHaveBeenCalledWith({
      entries: [{ key: "update.skipped_version", value: "v0.9.2" }],
    });
    expect(useUpdateStore.getState().phase).toEqual({
      kind: "available",
      info: INFO,
    });
  });

  it("Given 该版本已被跳过, When 求「待通告版本」, Then 为空但「有未跳过的更新」也为空", async () => {
    useUpdateStore.setState({
      phase: { kind: "available", info: INFO },
      skippedVersion: "v0.9.2",
      lastTrigger: "tick",
    });

    const s = useUpdateStore.getState();
    expect(pendingAnnouncement(s)).toBeNull();
    expect(unskippedUpdate(s)).toBeNull();
  });

  it("Given 跳过的是旧版本, When 出现更高版本, Then 重新计入通告与红点", async () => {
    useUpdateStore.setState({
      phase: { kind: "available", info: INFO },
      skippedVersion: "v0.9.1",
      lastTrigger: "tick",
    });

    const s = useUpdateStore.getState();
    expect(pendingAnnouncement(s)).toEqual(INFO);
    expect(unskippedUpdate(s)).toEqual(INFO);
  });

  it("Given 用户主动检查发现新版本, When 求「待通告版本」, Then 为空——他正看着结果", async () => {
    useUpdateStore.setState({
      phase: { kind: "available", info: INFO },
      lastTrigger: "manual",
    });

    expect(pendingAnnouncement(useUpdateStore.getState())).toBeNull();
    // 红点与 toast 不同：主动检查发现的更新照样要在设置入口上留标记。
    expect(unskippedUpdate(useUpdateStore.getState())).toEqual(INFO);
  });

  it("Given init 时已持久化跳过版本, When 载入, Then 读进 store", async () => {
    installBindings({
      GetAppSetting: vi.fn(() =>
        Promise.resolve({ key: "update.skipped_version", value: "v0.9.2" }),
      ),
    });

    await useUpdateStore.getState().init();

    expect(useUpdateStore.getState().skippedVersion).toBe("v0.9.2");
  });
});

describe("useUpdateWatch 回窗补检", () => {
  it("Given 已挂载, When 窗口重新获得焦点, Then 走受节流的入口补查一次", async () => {
    const app = installBindings();
    renderHook(() => useUpdateWatch());
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());

    window.dispatchEvent(new Event("focus"));

    await waitFor(() =>
      expect(app.MaybeCheckForUpdate).toHaveBeenCalledTimes(1),
    );
    // 回窗不该绕过节流 —— 频繁切窗口不能变成对 GitHub 的高频请求。
    expect(app.CheckForUpdate).not.toHaveBeenCalled();
  });

  it("Given StrictMode 把 effect 挂了两遍, When 第一次的解绑姗姗来迟, Then 订阅仍然活着", async () => {
    // init() 要跨一个 await 才交出解绑函数，而 StrictMode 的「挂载→卸载→再挂载」
    // 是同一批同步跑完的：第一次的解绑因此落在第二次订阅**之后**。它若按事件名
    // 一刀切，就会连第二次的订阅一起清掉 —— 开发态下更新胶囊从此收不到任何后台
    // 检查结果。
    const resolvers: Array<(v: { value: string }) => void> = [];
    installBindings({
      GetAppSetting: vi.fn(
        () => new Promise<{ value: string }>((res) => resolvers.push(res)),
      ),
    });

    renderHook(() => useUpdateWatch(), { wrapper: StrictMode });

    expect(resolvers).toHaveLength(2); // 前提：StrictMode 确实挂了两遍
    await act(async () => {
      resolvers.forEach((res) => res({ value: "" }));
    });

    await waitFor(() => expect(liveListeners("update:checked")).toBe(1));
    emitChecked({ trigger: "tick", info: INFO, error: "" });
    expect(useUpdateStore.getState().phase).toEqual({
      kind: "available",
      info: INFO,
    });
  });

  it("Given 已卸载, When 窗口获得焦点, Then 不再补查", async () => {
    const app = installBindings();
    const { unmount } = renderHook(() => useUpdateWatch());
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());

    unmount();
    window.dispatchEvent(new Event("focus"));

    expect(app.MaybeCheckForUpdate).not.toHaveBeenCalled();
  });
});
