import { renderHook, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const runtimeMocks = vi.hoisted(() => ({
  EventsOff: vi.fn(),
  EventsOn: vi.fn(),
}));

vi.mock("../../../wailsjs/runtime/runtime", () => runtimeMocks);
vi.mock("../../../wailsjs/go/app/App", () => ({
  GetAppSetting: vi.fn(),
  UpdateAppSettings: vi.fn(),
}));

import { GetAppSetting, UpdateAppSettings } from "../../../wailsjs/go/app/App";
import {
  INITIAL_UPDATE_STATE,
  pendingAnnouncement,
  unskippedUpdate,
  useUpdateStore,
  useUpdateWatch,
  type UpdateCheckOutcome,
} from "../update-store";

const getSettingMock = vi.mocked(GetAppSetting);
const updateSettingsMock = vi.mocked(UpdateAppSettings);

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
    ...overrides,
  };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { app: { App: app } },
  });
  return app;
}

// emitChecked 取出 init() 注册的 "update:checked" 处理器并投一条后台检查结果进去。
function emitChecked(outcome: UpdateCheckOutcome) {
  const entry = runtimeMocks.EventsOn.mock.calls.find(
    (c) => c[0] === "update:checked",
  );
  if (!entry) throw new Error("update:checked 未被订阅");
  (entry[1] as (o: UpdateCheckOutcome) => void)(outcome);
}

function emitProgress(downloaded: number, total: number) {
  const entry = runtimeMocks.EventsOn.mock.calls.find(
    (c) => c[0] === "update:progress",
  );
  if (!entry) throw new Error("update:progress 未被订阅");
  (entry[1] as (p: { downloaded: number; total: number }) => void)({
    downloaded,
    total,
  });
}

beforeEach(() => {
  vi.clearAllMocks();
  useUpdateStore.setState({ ...INITIAL_UPDATE_STATE });
  getSettingMock.mockRejectedValue(new Error("AppSettingNotFound"));
  updateSettingsMock.mockResolvedValue({} as never);
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

    expect(runtimeMocks.EventsOff).toHaveBeenCalledWith("update:checked");
    expect(runtimeMocks.EventsOff).toHaveBeenCalledWith("update:progress");
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

  it("Given 正在检查, When 再次触发, Then 不重复发起", async () => {
    const app = installBindings();
    useUpdateStore.setState({ phase: { kind: "checking" } });

    await useUpdateStore.getState().check("focus");

    expect(app.MaybeCheckForUpdate).not.toHaveBeenCalled();
  });
});

describe("update-store 下载与进度", () => {
  it("Given 有新版本, When 开始下载, Then 进度事件推进 downloading 百分比", async () => {
    await useUpdateStore.getState().init();
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    // download() 同步跑到第一个 await 就挂起，此时可以确定地投一条进度事件进去。
    const pending = useUpdateStore.getState().download(false);
    emitProgress(50, 200);

    expect(useUpdateStore.getState().phase).toEqual({
      kind: "downloading",
      info: INFO,
      progress: 25,
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
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    await useUpdateStore.getState().skipCurrentVersion();

    expect(updateSettingsMock).toHaveBeenCalledWith({
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
    getSettingMock.mockResolvedValue({
      key: "update.skipped_version",
      value: "v0.9.2",
    } as never);

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

  it("Given 已卸载, When 窗口获得焦点, Then 不再补查", async () => {
    const app = installBindings();
    const { unmount } = renderHook(() => useUpdateWatch());
    await waitFor(() => expect(runtimeMocks.EventsOn).toHaveBeenCalled());

    unmount();
    window.dispatchEvent(new Event("focus"));

    expect(app.MaybeCheckForUpdate).not.toHaveBeenCalled();
  });
});
