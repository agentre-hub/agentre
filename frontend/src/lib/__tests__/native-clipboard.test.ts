import {
  copyImageToClipboard,
  copyTextToClipboard,
  setClipboardImageWriter,
  setClipboardWriter,
} from "@agentre-hub/agentre-ui";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import i18n from "@/i18n";

import { CopyImageToClipboard } from "../../../wailsjs/go/app/App";
import { ClipboardSetText } from "../../../wailsjs/runtime/runtime";
import { installNativeClipboard } from "../native-clipboard";

vi.mock("../../../wailsjs/runtime/runtime", () => ({
  ClipboardSetText: vi.fn(),
}));

/*
  桌面端的复制不走 WebView 的 Clipboard API：WKWebView 只在它认定「正在处理
  用户手势」时放行 writeText，否则拒成 NotAllowedError，用户看到的就是
  "The request is not allowed by the user agent or the platform…"。
  装上原生通道后，共享包里的每个复制入口都改写系统剪贴板，不再看手势。
*/
describe("installNativeClipboard", () => {
  const originalClipboard = navigator.clipboard;
  const webWriteText = vi.fn();

  beforeEach(() => {
    vi.mocked(ClipboardSetText).mockReset();
    webWriteText.mockReset().mockRejectedValue(new Error("NotAllowedError"));
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText: webWriteText },
    });
    installNativeClipboard();
  });

  afterEach(() => {
    setClipboardWriter(null);
    setClipboardImageWriter(null);
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: originalClipboard,
    });
  });

  it("Given the native clipboard is installed, When text is copied, Then it is written through Wails and the WebView clipboard is not touched", async () => {
    vi.mocked(ClipboardSetText).mockResolvedValue(true);

    await expect(copyTextToClipboard("agentred run")).resolves.toBe(true);
    expect(ClipboardSetText).toHaveBeenCalledWith("agentred run");
    expect(webWriteText).not.toHaveBeenCalled();
  });

  it("Given the native write fails in Go, When text is copied, Then the Go error reaches the caller", async () => {
    vi.mocked(ClipboardSetText).mockRejectedValue("exit status 1");

    await expect(copyTextToClipboard("agentred run")).rejects.toThrow(
      "exit status 1",
    );
  });

  it("Given the native write reports false, When text is copied, Then the caller gets an explained failure instead of a false success", async () => {
    vi.mocked(ClipboardSetText).mockResolvedValue(false);

    await expect(copyTextToClipboard("agentred run")).rejects.toThrow(
      i18n.t("clipboard.writeRefused"),
    );
  });

  describe("images", () => {
    // 1×1 透明 PNG。
    const PNG_BASE64 =
      "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNkYPhfDwAChwGA60e6kgAAAABJRU5ErkJggg==";
    const webWrite = vi.fn();

    beforeEach(() => {
      vi.mocked(CopyImageToClipboard).mockReset();
      webWrite.mockReset();
      Object.defineProperty(navigator, "clipboard", {
        configurable: true,
        value: { write: webWrite, writeText: webWriteText },
      });
    });

    it("Given the native clipboard is installed, When an image is copied, Then the PNG goes to Go as base64 and the WebView clipboard is not touched", async () => {
      vi.mocked(CopyImageToClipboard).mockResolvedValue(undefined);

      await copyImageToClipboard({
        dataUrl: `data:image/png;base64,${PNG_BASE64}`,
      });

      expect(CopyImageToClipboard).toHaveBeenCalledWith(PNG_BASE64);
      expect(webWrite).not.toHaveBeenCalled();
    });

    it("Given the Go write fails, When an image is copied, Then the Go error reaches the caller", async () => {
      vi.mocked(CopyImageToClipboard).mockRejectedValue(
        "clipboardimg: NSPasteboard setData failed (step 2)",
      );

      await expect(
        copyImageToClipboard({
          dataUrl: `data:image/png;base64,${PNG_BASE64}`,
        }),
      ).rejects.toThrow("NSPasteboard setData failed");
    });
  });
});
