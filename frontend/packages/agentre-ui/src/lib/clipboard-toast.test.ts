import { describe, expect, it, vi, beforeEach, afterEach } from "vitest";

const sonnerMocks = vi.hoisted(() => ({
  toast: {
    error: vi.fn(),
    success: vi.fn(),
  },
}));

vi.mock("sonner", () => sonnerMocks);

import {
  installClipboard,
  installCopyCommandModel,
  installExecCommand,
  installFocusTrap,
  removeClipboard,
  restoreClipboardEnv,
} from "./__testing__/clipboard";
import {
  COPY_TOAST_DURATION_MS,
  COPY_TOAST_ERROR_DURATION_MS,
  copyTextToClipboard,
  copyTextWithToast,
} from "./clipboard-toast";

describe("copyTextWithToast", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    restoreClipboardEnv();
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
    const selectedAtCopy = installCopyCommandModel();

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
    // 借来的那个节点得还回去，别在 body 里留渣
    expect(document.body.childElementCount).toBe(0);
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
    expect(document.body.childElementCount).toBe(0);
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
    expect(document.body.childElementCount).toBe(0);
  });
});

/**
 * 不带 toast 的那一层。调用方自己有就地反馈（比如按钮上的「已复制」）时用它，
 * 免得同一次点击既翻按钮文案又弹一条 toast。
 */
describe("copyTextToClipboard", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  afterEach(() => {
    restoreClipboardEnv();
  });

  it("Given a writable clipboard, When text is copied, Then it goes through the Clipboard API and reports success", async () => {
    const writeText = vi.fn().mockResolvedValue(undefined);
    installClipboard(writeText);

    await expect(copyTextToClipboard("agentred run")).resolves.toBe(true);
    expect(writeText).toHaveBeenCalledWith("agentred run");
    expect(sonnerMocks.toast.success).not.toHaveBeenCalled();
    expect(sonnerMocks.toast.error).not.toHaveBeenCalled();
  });

  it("Given no Clipboard API, When text is copied, Then execCommand copies the selection and success is reported", async () => {
    removeClipboard();
    const selectedAtCopy = installCopyCommandModel();

    await expect(copyTextToClipboard("agentred run")).resolves.toBe(true);
    expect(selectedAtCopy).toEqual(["agentred run"]);
    expect(document.body.childElementCount).toBe(0);
  });

  /*
    兜底路要在**焦点陷阱里**也能复制。

    这条不是假想的边界：控制台部署在 http://<局域网 IP>:port 上（非安全上下文，
    没有 Clipboard API），「复制会话号」那条菜单项开在 Radix 下拉里，`onSelect`
    执行时菜单还没关、FocusScope 还在。谁往容器外 focus() 都会被同步抢回去，
    于是靠「focus 一个临时 textarea 再 select()」的做法什么都没选中 ——
    而 Chromium 此时 `execCommand("copy")` 仍返回 true，上层照样弹「已复制」，
    用户拿着一个没变过的剪贴板去粘贴。所以复制要走**文档选区**，不靠焦点。
  */
  it("Given a focus trap around the caller, When text is copied without the Clipboard API, Then the text still reaches the clipboard", async () => {
    removeClipboard();
    const selectedAtCopy = installCopyCommandModel();
    const releaseTrap = installFocusTrap();

    try {
      await expect(copyTextToClipboard("agentred run")).resolves.toBe(true);
      expect(selectedAtCopy).toEqual(["agentred run"]);
    } finally {
      releaseTrap();
    }
  });

  /*
    复制是插进来的一手，别把用户自己选中的东西吃掉：转录里选了一段话再从菜单
    复制会话号，选区应当还在原处。
  */
  it("Given the user already selected something, When text is copied without the Clipboard API, Then that selection is put back", async () => {
    removeClipboard();
    installCopyCommandModel();
    const paragraph = document.createElement("p");
    paragraph.textContent = "user's own selection";
    document.body.appendChild(paragraph);
    const own = document.createRange();
    own.selectNodeContents(paragraph);
    window.getSelection()?.removeAllRanges();
    window.getSelection()?.addRange(own);

    try {
      await expect(copyTextToClipboard("agentred run")).resolves.toBe(true);
      expect(String(window.getSelection() ?? "")).toBe("user's own selection");
    } finally {
      paragraph.remove();
      window.getSelection()?.removeAllRanges();
    }
  });

  it("Given neither Clipboard API nor a working execCommand, When text is copied, Then it reports failure instead of throwing", async () => {
    removeClipboard();
    installExecCommand(vi.fn().mockReturnValue(false));

    await expect(copyTextToClipboard("agentred run")).resolves.toBe(false);
  });

  it("Given a clipboard that rejects, When text is copied, Then the rejection reaches the caller", async () => {
    installClipboard(vi.fn().mockRejectedValue(new Error("denied")));

    await expect(copyTextToClipboard("agentred run")).rejects.toThrow("denied");
  });
});
