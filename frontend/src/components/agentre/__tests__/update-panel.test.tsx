import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../../wailsjs/runtime/runtime", async () => {
  const actual = await vi.importActual<
    typeof import("../../../../wailsjs/runtime/runtime")
  >("../../../../wailsjs/runtime/runtime");
  return {
    ...actual,
    EventsOn: vi.fn(),
    EventsOff: vi.fn(),
    BrowserOpenURL: vi.fn(),
  };
});

import { BrowserOpenURL } from "../../../../wailsjs/runtime/runtime";
import { INITIAL_UPDATE_STATE, useUpdateStore } from "@/stores/update-store";

import { UpdatePanel } from "../update-panel";

const INFO = {
  hasUpdate: true,
  currentVersion: "0.9.1",
  latestVersion: "v0.9.2",
  releaseNotes: "· 修了后台任务卡 running\n· 修了切 tab 空白",
  releaseURL: "https://example/releases/v0.9.2",
  publishedAt: "2026-08-17T00:00:00Z",
};

type AppMock = Record<string, ReturnType<typeof vi.fn>>;

function installBindings(overrides?: AppMock): AppMock {
  const app: AppMock = {
    GetUpdateChannel: vi.fn(() => Promise.resolve("stable")),
    CheckForUpdate: vi.fn(() => Promise.resolve(INFO)),
    MaybeCheckForUpdate: vi.fn(() => Promise.resolve(INFO)),
    DownloadAndInstallUpdate: vi.fn(() => Promise.resolve()),
    RestartApp: vi.fn(() => Promise.resolve()),
    UpdateAppSettings: vi.fn(() => Promise.resolve({})),
    ...overrides,
  };
  Object.defineProperty(window, "go", {
    configurable: true,
    value: { app: { App: app } },
  });
  return app;
}

beforeEach(() => {
  vi.clearAllMocks();
  useUpdateStore.setState({ ...INITIAL_UPDATE_STATE });
  installBindings();
});

describe("更新面板 · 有新版本", () => {
  beforeEach(() => {
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });
  });

  it("Given 有新版本, When 打开面板, Then 版本号、发布时间、当前通道与更新说明都在", async () => {
    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);

    expect(await screen.findByText(/v0\.9\.2/)).toBeInTheDocument();
    expect(screen.getByText(/2026-08-17/)).toBeInTheDocument();
    expect(await screen.findByText(/Stable/i)).toBeInTheDocument();
    expect(screen.getByText(/切 tab 空白/)).toBeInTheDocument();
  });

  it("Given 面板打开, When 点下载并安装, Then 走 store 的下载动作", async () => {
    const app = installBindings();
    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);

    fireEvent.click(screen.getByRole("button", { name: /Download/i }));

    await waitFor(() =>
      expect(app.DownloadAndInstallUpdate).toHaveBeenCalledWith(false),
    );
  });

  it("Given 面板打开, When 点跳过此版本, Then 胶囊陈述不变、只压制通告", async () => {
    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);

    fireEvent.click(screen.getByRole("button", { name: /Skip/i }));

    await waitFor(() =>
      expect(useUpdateStore.getState().skippedVersion).toBe("v0.9.2"),
    );
    expect(useUpdateStore.getState().phase).toEqual({
      kind: "available",
      info: INFO,
    });
  });

  it("Given 持久化失败, When 点跳过此版本, Then 不谎称已跳过", async () => {
    installBindings({
      UpdateAppSettings: vi.fn(() => Promise.reject(new Error("db closed"))),
    });
    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);

    fireEvent.click(screen.getByRole("button", { name: /Skip/i }));

    await waitFor(() => expect(useUpdateStore.getState().phase).toBeTruthy());
    expect(useUpdateStore.getState().skippedVersion).toBe("");
  });

  it("Given 面板打开, When 点发布页, Then 用外部浏览器打开", async () => {
    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);

    fireEvent.click(
      screen.getByRole("button", { name: /Release notes|Release page/i }),
    );

    expect(BrowserOpenURL).toHaveBeenCalledWith(INFO.releaseURL);
  });
});

describe("更新面板 · 其余四态", () => {
  it("Given 正在下载, When 渲染, Then 显示已下载/总量与百分比而不是下载按钮", () => {
    useUpdateStore.setState({
      phase: {
        kind: "downloading",
        info: INFO,
        progress: 42,
        downloaded: 12 * 1024 * 1024,
        total: 48 * 1024 * 1024,
      },
    });

    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);

    expect(screen.getByText("42%")).toBeInTheDocument();
    // 百分比说不出「还要等多久」,字节数才说得出。
    expect(screen.getByText("12.0 MB / 48.0 MB")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: /Download and install/i }),
    ).not.toBeInTheDocument();
  });

  it("Given 已安装待重启, When 渲染, Then 给出重启入口并说明会结束运行中的会话", () => {
    useUpdateStore.setState({ phase: { kind: "installed", info: INFO } });

    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);

    expect(
      screen.getByRole("button", { name: /Restart now/i }),
    ).toBeInTheDocument();
    expect(screen.getByText(/running sessions/i)).toBeInTheDocument();
  });

  it("Given 已安装待重启, When 点稍后, Then 收起面板而不是重启", () => {
    useUpdateStore.setState({
      phase: { kind: "installed", info: INFO },
      panelOpen: true,
    });

    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: /Later/i }));

    expect(useUpdateStore.getState().panelOpen).toBe(false);
    expect(useUpdateStore.getState().phase).toEqual({
      kind: "installed",
      info: INFO,
    });
  });

  it("Given 已是最新, When 渲染, Then 副行给出真的当前版本而不是「未知」", async () => {
    useUpdateStore.setState({ phase: { kind: "uptodate" } });

    render(<UpdatePanel version="0.9.1" onOpenSettings={() => {}} />);

    expect(await screen.findByText(/Current v0\.9\.1/)).toBeInTheDocument();
  });

  it("Given 已是最新, When 点重新检查, Then 走绕过节流的检查", async () => {
    const app = installBindings();
    useUpdateStore.setState({ phase: { kind: "uptodate" } });

    render(<UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />);
    fireEvent.click(screen.getByRole("button", { name: /Check again/i }));

    await waitFor(() => expect(app.CheckForUpdate).toHaveBeenCalled());
  });

  it("Given 检查失败, When 渲染, Then 原始错误可选中复制且没有复制按钮", () => {
    useUpdateStore.setState({
      phase: {
        kind: "error",
        message: "dial tcp 140.82.114.6:443: i/o timeout",
      },
    });

    const { container } = render(
      <UpdatePanel version="v0.9.1" onOpenSettings={() => {}} />,
    );

    const detail = screen.getByText(/dial tcp 140\.82\.114\.6:443/);
    expect(detail.closest("[data-selectable-text='true']")).not.toBeNull();
    expect(container.querySelector("button[aria-label*='opy']")).toBeNull();
  });

  it("Given 任一终态, When 点打开更新设置, Then 通知宿主走深链并收起面板", () => {
    const onOpenSettings = vi.fn();
    useUpdateStore.setState({ phase: { kind: "uptodate" }, panelOpen: true });

    render(<UpdatePanel version="v0.9.1" onOpenSettings={onOpenSettings} />);
    fireEvent.click(screen.getByRole("button", { name: /update settings/i }));

    expect(onOpenSettings).toHaveBeenCalledTimes(1);
    // 深链只是换路由,状态栏不重挂:面板不自己收起来就会一直浮在设置页上面。
    expect(useUpdateStore.getState().panelOpen).toBe(false);
  });
});
