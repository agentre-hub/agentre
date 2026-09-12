import { vi } from "vitest";

/**
 * 复制这件事在测试里的公共替身。
 *
 * 复制有两条路：安全上下文走 Clipboard API，非安全上下文（宿主部署在
 * `http://<局域网 IP>:port` 上）整个 `navigator.clipboard` 都不存在，只剩
 * `execCommand` 兜底。真正会出事的是后一条，而它对「谁拿着焦点」「选中了什么」
 * 敏感——所以任何摸剪贴板的用例都该用同一套替身，别各写各的。
 */

const originalClipboard = navigator.clipboard;
const originalExecCommand = document.execCommand;

export function installClipboard(writeText: ReturnType<typeof vi.fn>) {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
}

/** 非安全上下文下浏览器根本不暴露 navigator.clipboard——不是拒绝，是没有。 */
export function removeClipboard() {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: undefined,
  });
}

export function installExecCommand(execCommand: unknown) {
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: execCommand,
  });
}

/** 把两样都还原。测试文件在 afterEach 里调，免得污染同一个文件里后面的用例。 */
export function restoreClipboardEnv() {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: originalClipboard,
  });
  Object.defineProperty(document, "execCommand", {
    configurable: true,
    value: originalExecCommand,
  });
}

/**
 * `execCommand("copy")` 的替身：按浏览器的规则算出「这一刻按下复制会拿走什么」
 * ——焦点在可编辑控件里时是那个控件的选区，否则是文档选区。返回的数组按调用
 * 顺序记下每次真正会被复制走的内容。
 *
 * 断言必须打在它上面，不能打在返回值上：Chromium 在**什么都没选中**时
 * `execCommand("copy")` 照样返回 `true`（2026-09-02 在真 Chromium 上实测）。
 * 所以这个替身也恒返回 `true` —— 只有「真的会复制到什么」才说明问题。
 */
export function installCopyCommandModel(): string[] {
  const copied: string[] = [];
  installExecCommand(
    vi.fn((command: string) => {
      if (command !== "copy") return false;
      const active = document.activeElement;
      const field =
        active instanceof HTMLTextAreaElement ||
        active instanceof HTMLInputElement
          ? active.value.slice(
              active.selectionStart ?? 0,
              active.selectionEnd ?? 0,
            )
          : "";
      copied.push(field || String(window.getSelection() ?? ""));
      return true;
    }),
  );
  return copied;
}

/**
 * Radix 的 FocusScope（下拉菜单、对话框都用它）：焦点一落到容器外就被同步抢回来。
 * 返回撤除函数。
 */
export function installFocusTrap(): () => void {
  const trap = document.createElement("button");
  document.body.appendChild(trap);
  trap.focus();
  const steal = (event: FocusEvent) => {
    if (event.target !== trap) trap.focus();
  };
  document.addEventListener("focusin", steal, true);
  return () => {
    document.removeEventListener("focusin", steal, true);
    trap.remove();
  };
}
