import { describe, expect, it, vi, beforeEach } from "vitest";
import { renderHook } from "@testing-library/react";

const navigate = vi.fn();

vi.mock("react-router-dom", () => ({ useNavigate: () => navigate }));

vi.mock("../../../../wailsjs/go/app/App", () => ({
  AnswerToolApproval: vi.fn(),
  AnswerToolPermission: vi.fn(),
  AnswerUserQuestion: vi.fn(),
  OpenPath: vi.fn(),
  ResolveExecApproval: vi.fn(),
  ResolvePlanAction: vi.fn(),
  WorkspaceFsReadFile: vi.fn(),
}));

vi.mock("../../../../wailsjs/runtime/runtime", () => ({
  BrowserOpenURL: vi.fn(),
}));

import { OpenPath } from "../../../../wailsjs/go/app/App";
import { desktopTranscriptPorts } from "../transcript-ports-desktop";
import { useDesktopTranscriptPorts } from "../transcript-ports-desktop";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useFileSettingsStore } from "@/stores/file-settings-store";

beforeEach(() => {
  navigate.mockReset();
  vi.mocked(OpenPath).mockReset();
  localStorage.clear();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useFileSettingsStore.setState({
    settings: { openAction: "preview" },
  });
});

// 每种 @ 提及都有自己的去处。设备曾经会落进「不是 agent 就是项目」的 else 分支,
// 于是点一台机器会把人送到项目页 —— 这个用例守的就是那一步。
describe("useDesktopTranscriptPorts 的 openMention", () => {
  it.each([
    ["agent", "/org", undefined],
    ["project", "/projects", undefined],
    ["device", "/settings", { state: { settingsPage: "remote-devices" } }],
  ] as const)(
    "Given a %s mention chip, When it is clicked, Then the desktop routes to its own page",
    (kind, path, options) => {
      const { result } = renderHook(() => useDesktopTranscriptPorts());

      result.current.openMention?.({ kind, refId: 1, label: "x" });

      expect(navigate).toHaveBeenCalledWith(
        path,
        ...(options ? [options] : []),
      );
    },
  );
});

// previewFile 是桌面端与共享包之间「转录点一条文件路径该去哪」的唯一接缝
// (spec「两端的入口」)。设置读取留在这里,包内的 dispatchClick 只认布尔回执。
describe("desktopTranscriptPorts 的 previewFile", () => {
  // 转录点开的预览要回答的是「这个文件**现在**长什么样」(spec「入口与可用性」:
  // 「显示该文件当前在那台机器上的内容」),所以入口模式是 directory —— 面板据此
  // 走注入的 readFile 端口读工作区正文。"session" 是侧栏「本次会话」那一档的
  // 工具 diff:那一档一次取数都不打,画的是重放出来的改动,不是当前内容。
  it("Given files.open_action is preview, When previewFile is called, Then it opens the tab in the mode that reads the file's current content", () => {
    useFileSettingsStore.setState({ settings: { openAction: "preview" } });

    const tookOver = desktopTranscriptPorts.previewFile(7, "src/foo.go");

    expect(tookOver).toBe(true);
    expect(
      useFilePreviewTabsStore.getState().previewTabsBySession[7]?.tabs,
    ).toEqual([
      expect.objectContaining({ path: "src/foo.go", sourceMode: "directory" }),
    ]);
    expect(OpenPath).not.toHaveBeenCalled();
  });

  it("Given files.open_action is external, When previewFile is called, Then it declines and leaves no preview tab behind", () => {
    useFileSettingsStore.setState({ settings: { openAction: "external" } });

    const tookOver = desktopTranscriptPorts.previewFile(7, "src/foo.go");

    expect(tookOver).toBe(false);
    expect(
      useFilePreviewTabsStore.getState().previewTabsBySession[7],
    ).toBeUndefined();
    expect(OpenPath).not.toHaveBeenCalled();
  });
});
