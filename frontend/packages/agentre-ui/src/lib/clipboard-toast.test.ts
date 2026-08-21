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
const originalExecCommand = document.execCommand;

function installClipboard(writeText: ReturnType<typeof vi.fn>) {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
}

/** 非安全上下文下浏览器根本不暴露 navigator.clipboard——不是拒绝，是没有。 */
function removeClipboard() {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: undefined,
  });
}

function installExecCommand(execCommand: unknown) {
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: execCommand,
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
    Object.defineProperty(document, "execCommand", {
      configurable: true,
      value: originalExecCommand,
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

  it("Given no Clipboard API, When text is copied, Then execCommand copies the selection and success is reported", async () => {
    removeClipboard();
    // execCommand 复制的是「当前选区」，所以真正要断言的是调用发生那一刻选中的是什么
    const selectedAtCopy: string[] = [];
    const execCommand = vi.fn((command: string) => {
      if (command !== "copy") return false;
      selectedAtCopy.push(
        (document.activeElement as HTMLTextAreaElement | null)?.value ?? "",
      );
      return true;
    });
    installExecCommand(execCommand);

    const copied = await copyTextWithToast("agentred run", {
      successTitle: "已复制命令",
      successDescription: "粘贴到终端即可运行",
    });

    expect(copied).toBe(true);
    expect(selectedAtCopy).toEqual(["agentred run"]);
    expect(sonnerMocks.toast.success).toHaveBeenCalledWith(
      "已复制命令",
      expect.objectContaining({
        description: "粘贴到终端即可运行",
        duration: COPY_TOAST_DURATION_MS,
        position: "bottom-right",
      }),
    );
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
    // 借来的 textarea 得还回去，别在 body 里留渣
    expect(document.body.querySelector("textarea")).toBeNull();
  });

  it("Given no Clipboard API and a refusing execCommand, When text is copied, Then the error toast explains the requirement", async () => {
    removeClipboard();
    installExecCommand(vi.fn().mockReturnValue(false));

    const copied = await copyTextWithToast("agentred run", {
      errorTitle: "复制命令失败",
      successTitle: "已复制命令",
    });

    expect(copied).toBe(false);
    expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
      "复制命令失败",
      expect.objectContaining({
        description: "Copying requires HTTPS or localhost",
        duration: COPY_TOAST_ERROR_DURATION_MS,
        position: "bottom-right",
      }),
    );
    expect(sonnerMocks.toast.success).not.toHaveBeenCalled();
    expect(document.body.querySelector("textarea")).toBeNull();
  });

  it("Given neither Clipboard API nor execCommand, When text is copied, Then the same explanation is shown instead of a TypeError", async () => {
    removeClipboard();
    installExecCommand(undefined);

    const copied = await copyTextWithToast("agentred run", {
      errorTitle: "复制命令失败",
      successTitle: "已复制命令",
    });

    expect(copied).toBe(false);
    expect(sonnerMocks.toast.error).toHaveBeenCalledWith(
      "复制命令失败",
      expect.objectContaining({
        description: "Copying requires HTTPS or localhost",
        duration: COPY_TOAST_ERROR_DURATION_MS,
        position: "bottom-right",
      }),
    );
    expect(document.body.querySelector("textarea")).toBeNull();
  });
});
