import "@testing-library/jest-dom/vitest";

import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: { error: vi.fn(), success: vi.fn(), warning: vi.fn() },
}));
vi.mock("sonner", () => sonnerMocks);

const appMocks = vi.hoisted(() => ({
  readFile: vi.fn(),
  gitFileContent: vi.fn(),
  openPath: vi.fn(),
}));
vi.mock("@/../wailsjs/go/app/App", () => ({
  WorkspaceFsReadFile: (...args: unknown[]) => appMocks.readFile(...args),
  WorkspaceFsGitFileContent: (...args: unknown[]) =>
    appMocks.gitFileContent(...args),
  OpenPath: (...args: unknown[]) => appMocks.openPath(...args),
  RevealPath: vi.fn(),
}));

const loaderMocks = vi.hoisted(() => ({
  loadMonaco: vi.fn(async () => ({
    editor: {
      create: () => ({ setValue() {}, dispose() {} }),
      createDiffEditor: () => ({ setModel() {}, dispose() {} }),
      createModel: () => ({ dispose() {} }),
      setTheme() {},
    },
  })),
}));
vi.mock("@/lib/file-preview/monaco-loader", () => loaderMocks);

import { useChatSidebarStore } from "@/stores/chat-sidebar-store";
import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useSessionStatusStore } from "@/stores/session-status-store";

import { FilePreviewPanel } from "../file-preview-panel";

const CWD = "/w";

beforeEach(() => {
  localStorage.clear();
  useChatSidebarStore.setState({ workRootBySession: {} });
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
  useSessionStatusStore.getState().__reset();
  appMocks.readFile.mockReset();
  appMocks.readFile.mockResolvedValue({
    content: "<h1>Report</h1>",
    contentType: "",
    binary: false,
    tooLarge: false,
  });
  appMocks.gitFileContent.mockReset();
  appMocks.openPath.mockReset();
  appMocks.openPath.mockResolvedValue(undefined);
  sonnerMocks.toast.error.mockReset();
});

function renderHtmlTab() {
  useFilePreviewTabsStore
    .getState()
    .openPreview(7, "out/report.html", "directory");
  return render(<FilePreviewPanel sessionId={7} cwd={CWD} />);
}

describe("FilePreviewPanel · 用浏览器打开 HTML", () => {
  // Given 本地会话里开着一个 HTML 标签,
  // When 点 header 上的「用浏览器打开」,
  // Then 以系统默认应用打开该文件在本机上的绝对路径。
  it("opens the file's absolute path with the system app in a local session", async () => {
    renderHtmlTab();

    const panel = await screen.findByRole("complementary", {
      name: "File preview",
    });
    await userEvent.click(
      await within(panel).findByRole("button", { name: "Open in browser" }),
    );
    expect(appMocks.openPath).toHaveBeenCalledWith("/w/out/report.html");
  });

  // Given 远端会话（文件通常在另一台机器上）,
  // Then 桌面端照样给出「用浏览器打开」（spec：宿主有系统打开能力即出现，只有
  // 控制台不渲染；与浮窗「系统打开」在远端会话同样出现一致）；本机没有该文件时
  // 说事实，不贴系统原话。
  it("still offers it in a remote session and states the fact when the file is not on this machine", async () => {
    appMocks.openPath.mockResolvedValue({ unavailable: "not-found" });
    renderHtmlTab();

    const panel = await screen.findByRole("complementary", {
      name: "File preview",
    });
    await userEvent.click(
      await within(panel).findByRole("button", { name: "Open in browser" }),
    );
    expect(appMocks.openPath).toHaveBeenCalledWith("/w/out/report.html");
    await vi.waitFor(() =>
      expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
        "This file isn't on this machine",
      ),
    );
  });
});
