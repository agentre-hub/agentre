import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../../wailsjs/runtime/runtime", async () => {
  const actual = await vi.importActual<
    typeof import("../../../../wailsjs/runtime/runtime")
  >("../../../../wailsjs/runtime/runtime");
  return { ...actual, EventsOn: vi.fn(), EventsOff: vi.fn() };
});

import { AppStatusBar } from "@/components/agentre";
import {
  INITIAL_UPDATE_STATE,
  useUpdateStore,
  type UpdatePhase,
} from "@/stores/update-store";

const INFO = {
  hasUpdate: true,
  currentVersion: "0.9.1",
  latestVersion: "v0.9.2",
  releaseNotes: "",
  releaseURL: "",
  publishedAt: "",
};

function renderStatusBar(onOpenUpdateSettings?: () => void) {
  return render(
    <AppStatusBar
      agentCount={4}
      runningCount={1}
      approvalCount={0}
      unreadCount={0}
      attentionIds={[]}
      onAttentionClick={() => {}}
      status="running"
      version="v0.9.1"
      onOpenUpdateSettings={onOpenUpdateSettings}
    />,
  );
}

beforeEach(() => {
  useUpdateStore.setState({ ...INITIAL_UPDATE_STATE });
});

describe("状态栏更新胶囊", () => {
  it("Given 从未检查过, When 渲染状态栏, Then 仍是今天那段灰色版本号", () => {
    renderStatusBar();

    // 「有更新」必须是一次真的状态跃迁 —— 没更新时不该有彩色胶囊常驻。
    expect(screen.getByText("v0.9.1")).toBeInTheDocument();
    expect(screen.queryByText(/available/i)).not.toBeInTheDocument();
  });

  it("Given 已查过且是最新, When 渲染, Then 同样退回灰色版本号", () => {
    useUpdateStore.setState({ phase: { kind: "uptodate" } });

    renderStatusBar();

    expect(screen.getByText("v0.9.1")).toBeInTheDocument();
  });

  it("Given 正在检查, When 渲染, Then 显示检查中且不可点", () => {
    useUpdateStore.setState({ phase: { kind: "checking" } });

    renderStatusBar();

    expect(screen.getByRole("button", { name: /Checking/i })).toBeDisabled();
  });

  it("Given 有新版本, When 渲染, Then 胶囊报出版本号", () => {
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    renderStatusBar();

    expect(
      screen.getByRole("button", { name: /v0\.9\.2/ }),
    ).toBeInTheDocument();
  });

  it("Given 正在下载, When 渲染, Then 胶囊带百分比", () => {
    useUpdateStore.setState({
      phase: { kind: "downloading", info: INFO, progress: 42 },
    });

    renderStatusBar();

    expect(screen.getByRole("button", { name: /42%/ })).toBeInTheDocument();
  });

  it("Given 已安装待重启, When 渲染, Then 胶囊提示重启", () => {
    useUpdateStore.setState({ phase: { kind: "installed", info: INFO } });

    renderStatusBar();

    expect(
      screen.getByRole("button", { name: /Restart/i }),
    ).toBeInTheDocument();
  });

  it("Given 检查失败, When 渲染, Then 胶囊显示失败", () => {
    useUpdateStore.setState({
      phase: { kind: "error", message: "i/o timeout" },
    });

    renderStatusBar();

    expect(screen.getByRole("button", { name: /failed/i })).toBeInTheDocument();
  });

  it("Given 有新版本, When 点击胶囊, Then 就地展开更新面板", async () => {
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    renderStatusBar();
    fireEvent.click(screen.getByRole("button", { name: /v0\.9\.2/ }));

    // 面板就在胶囊上方展开,不必先跳到设置页。
    expect(
      await screen.findByRole("button", { name: /Download and install/i }),
    ).toBeInTheDocument();
  });

  // 胶囊里的文案是缩写(「检查中」「下载中 42%」),读屏得听到状态与版本。
  const ARIA_CASES: Array<[string, UpdatePhase, RegExp]> = [
    ["从未检查过", { kind: "idle" }, /Current version v0\.9\.1/i],
    [
      "检查中",
      { kind: "checking" },
      /Checking for updates, current version v0\.9\.1/i,
    ],
    [
      "有新版本",
      { kind: "available", info: INFO },
      /Update v0\.9\.2 available/i,
    ],
    [
      "下载中",
      { kind: "downloading", info: INFO, progress: 42 },
      /Downloading v0\.9\.2, 42%/i,
    ],
    [
      "已安装待重启",
      { kind: "installed", info: INFO },
      /v0\.9\.2 installed, restart to finish/i,
    ],
    [
      "检查失败",
      { kind: "error", message: "i/o timeout" },
      /Update check failed, current version v0\.9\.1/i,
    ],
  ];

  it.each(ARIA_CASES)(
    "Given %s, When 读胶囊的无障碍名, Then 状态与版本都在里面",
    (_state, phase, expected) => {
      useUpdateStore.setState({ phase });

      renderStatusBar();

      expect(
        screen.getByRole("button", { name: expected }),
      ).toBeInTheDocument();
    },
  );

  it("Given 状态栏容器, When 渲染, Then 高度仍是 h-7 且未新增行", () => {
    useUpdateStore.setState({ phase: { kind: "available", info: INFO } });

    const { container } = renderStatusBar();

    const footer = container.querySelector("footer");
    expect(footer?.className).toContain("h-7");
  });
});
