import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

vi.mock("sonner", () => sonnerMocks);

import {
  COPY_TOAST_DURATION_MS,
  COPY_TOAST_ERROR_DURATION_MS,
  copyTextWithToast,
} from "./clipboard-toast";

const originalClipboard = navigator.clipboard;

function installClipboard(writeText: ReturnType<typeof vi.fn>) {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
}

describe("copyTextWithToast", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: originalClipboard,
    });
  });

  it("Given writable clipboard, When text is copied, Then Sonner shows a timed success toast", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    installClipboard(writeText);

    const copied = await copyTextWithToast("agentred run", {
      successTitle: "已复制命令",
      successDescription: "粘贴到终端即可运行",
    });

    expect(copied).toBe(true);
    expect(writeText).toHaveBeenCalledWith("agentred run");
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
      "已复制命令",
      expect.objectContaining({
        description: "粘贴到终端即可运行",
        duration: COPY_TOAST_DURATION_MS,
        position: "bottom-right",
      }),
    );
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });

  it("Given clipboard write fails, When text is copied, Then Sonner shows a timed error toast", async () => {
    const writeText = vi.fn().mockRejectedValue(new Error("denied"));
    installClipboard(writeText);

    const copied = await copyTextWithToast("agentred run", {
      errorTitle: "复制命令失败",
      successTitle: "已复制命令",
    });

    expect(copied).toBe(false);
    expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
      "复制命令失败",
      expect.objectContaining({
        description: "denied",
        duration: COPY_TOAST_ERROR_DURATION_MS,
        position: "bottom-right",
      }),
    );
    expect(sonnerMocks.toast.success).not.toHaveBeenCalled();
  });
});
