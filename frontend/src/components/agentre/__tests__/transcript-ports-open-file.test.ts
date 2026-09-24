import { beforeEach, describe, expect, it, vi } from "vitest";

// 打在生产路径上：桩的是真实 Wails 绑定，断言的是宿主适配器自己的转换。
const openPathMock = vi.fn();
vi.mock("../../../../wailsjs/go/app/App", () => ({
  OpenPath: (path: string) => openPathMock(path),
}));

import { useFilePreviewTabsStore } from "@/stores/file-preview-tabs-store";
import { useFileSettingsStore } from "@/stores/file-settings-store";

import { desktopTranscriptPorts } from "../transcript-ports-desktop";

function setOpenAction(openAction: "preview" | "external") {
  useFileSettingsStore.setState({ settings: { openAction } });
}

beforeEach(() => {
  openPathMock.mockReset();
  localStorage.clear();
  useFilePreviewTabsStore.setState({ previewTabsBySession: {} });
});

describe("desktopTranscriptPorts.openPath", () => {
  it("resolves when the system app took the file", async () => {
    openPathMock.mockResolvedValue({});

    await expect(
      desktopTranscriptPorts.openPath("/w/a.go:3"),
    ).resolves.toBeUndefined();
    expect(openPathMock).toHaveBeenCalledWith("/w/a.go:3");
  });

  // 远端会话的文件不在本机：后端在应答里说 not-found，端口把它还原成一次带
  // notFound 标记的失败，转录据此说事实而不是贴系统原话。
  it("rejects with a notFound failure when the path is not on this machine", async () => {
    openPathMock.mockResolvedValue({ unavailable: "not-found" });

    const err = await desktopTranscriptPorts
      .openPath("/remote/a.go")
      .catch((e: unknown) => e);
    expect((err as { kind?: unknown }).kind).toBe("notFound");
  });

  it("passes any other failure through", async () => {
    openPathMock.mockRejectedValue(new Error("boom"));

    await expect(desktopTranscriptPorts.openPath("/w/a.go")).rejects.toThrow(
      "boom",
    );
  });
});

describe("desktopTranscriptPorts.fileOpenDefault", () => {
  it.each(["preview", "external"] as const)(
    "reports the files.open_action setting (%s)",
    (openAction) => {
      setOpenAction(openAction);
      expect(desktopTranscriptPorts.fileOpenDefault()).toBe(openAction);
    },
  );
});

describe("desktopTranscriptPorts.previewFile", () => {
  it("declines when the setting says external", () => {
    setOpenAction("external");

    expect(desktopTranscriptPorts.previewFile(7, "a.go")).toBe(false);
    expect(useFilePreviewTabsStore.getState().previewTabsBySession[7]).toBe(
      undefined,
    );
  });

  // 用户在浮窗 / 失败提示里明确选了「预览」：设置不拦它。
  it("takes over when forced, even with the setting on external, keeping the anchor", () => {
    setOpenAction("external");

    expect(
      desktopTranscriptPorts.previewFile(
        7,
        "a.go",
        { line: 4 },
        { force: true },
      ),
    ).toBe(true);
    const tab =
      useFilePreviewTabsStore.getState().previewTabsBySession[7]?.tabs[0];
    expect(tab).toMatchObject({ path: "a.go", sourceMode: "directory" });
    expect(tab?.reveal).toMatchObject({ line: 4 });
  });
});
