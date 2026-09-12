import i18n from "i18next";
import { toast } from "sonner";

import { AGENTRE_UI_NAMESPACE } from "../i18n";

// 取的是 i18next 的**默认实例**——宿主(桌面端 src/i18n/index.ts)init 的正是它,
// 所以这里拿到的翻译状态与宿主始终同一份。两处 t() 都在函数体内求值,调用发生时
// 宿主早已 init 完毕;不要把它们提到模块顶层求值,那会退化成 import 期求值、
// 依赖模块求值顺序才碰巧能拿到译文。

export const COPY_TOAST_DURATION_MS = 5000;
export const COPY_TOAST_ERROR_DURATION_MS = 7000;

type CopyTextWithToastOptions = {
  errorTitle?: string;
  successDescription?: string;
  successTitle: string;
};

function errorMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  return String(err);
}

/**
 * 非安全上下文下的降级复制。
 *
 * Clipboard API 在规范里标了 `[SecureContext]`：页面不是 https / localhost 时
 * `navigator.clipboard` **整个对象都不存在**——不是权限被拒，所以没有任何授权
 * 可以去申请，弹不出授权框，也没有能点「允许」的地方。控制台被部署在
 * `http://<局域网 IP>:port` 上时走的正是这条路。
 *
 * `execCommand("copy")` 虽已废弃，但不受安全上下文限制，是这类页面上唯一还能
 * 把文本送进剪贴板的手段，所以留作兜底而不是直接报错了事。
 *
 * 选中的办法是**文档选区**（临时节点 + Range），不是「focus 一个 textarea 再
 * select()」：后者在焦点陷阱里必然空手而归 —— Radix 的 FocusScope（下拉菜单、
 * 对话框都用它）会把落到容器外的焦点同步抢回去，textarea 拿不到焦点，它的
 * `select()` 就只是设了自己的 selectionStart/End，文档选区仍然是空的。而 Chromium
 * 此时 `execCommand("copy")` **照样返回 true**，于是上层弹出「已复制」而剪贴板
 * 纹丝不动。文档选区不看焦点在谁身上，两种场合都成立。
 */
function copyViaExecCommand(text: string): boolean {
  if (typeof document.execCommand !== "function") return false;

  const selection = window.getSelection();
  if (!selection) return false;

  const carrier = document.createElement("span");
  carrier.textContent = text;
  // 不能用 display:none / hidden：不在渲染树里的节点选不中，execCommand 会空手而归。
  // 挪出视口即可；`pre` 保住换行与连续空格（复制的常常是整条命令）。
  carrier.style.position = "fixed";
  carrier.style.top = "-9999px";
  carrier.style.whiteSpace = "pre";
  // 祖先上的 user-select:none 会连带让这里选不中（滚动锁、菜单容器都爱设它）。
  carrier.style.userSelect = "text";
  carrier.style.setProperty("-webkit-user-select", "text");
  document.body.appendChild(carrier);

  // 复制是插进来的一手，用户自己选中的那段得原样还回去。
  const restore = Array.from({ length: selection.rangeCount }, (_, i) =>
    selection.getRangeAt(i),
  );

  try {
    const range = document.createRange();
    range.selectNodeContents(carrier);
    selection.removeAllRanges();
    selection.addRange(range);
    // 选区是空的就别发命令：那种情况下的 true 是假的（见上），谎报成功比复制
    // 失败更糟——用户会带着一个没变过的剪贴板去粘贴。
    if (String(selection) === "") return false;
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    selection.removeAllRanges();
    for (const range of restore) selection.addRange(range);
    carrier.remove();
  }
}

/**
 * 复制本身，不带任何反馈。
 *
 * 调用方已经有就地反馈时（`AddDeviceGuide` 的复制按钮会翻成「已复制」）用这一层，
 * 否则同一次点击会既翻按钮文案又弹一条 toast。要 toast 的走 `copyTextWithToast`。
 *
 * 返回 `false` 只表示「这个环境没有可用的复制通道」；Clipboard API 存在却拒绝
 * （文档失焦、权限被拒）时 **抛出**，由调用方决定怎么说——两者不是一回事，
 * 压成同一个 false 会让上层没法区分「不能复制」和「这次没复制成」。
 */
export async function copyTextToClipboard(text: string): Promise<boolean> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return true;
  }
  return copyViaExecCommand(text);
}

export async function copyTextWithToast(
  text: string,
  {
    errorTitle = i18n.t("common.copyFailed", { ns: AGENTRE_UI_NAMESPACE }),
    successDescription,
    successTitle,
  }: CopyTextWithToastOptions,
): Promise<boolean> {
  try {
    if (!(await copyTextToClipboard(text))) {
      throw new Error(
        i18n.t("clipboard.insecureContext", { ns: AGENTRE_UI_NAMESPACE }),
      );
    }
    toast.success(successTitle, {
      description: successDescription,
      duration: COPY_TOAST_DURATION_MS,
      position: "bottom-right",
    });
    return true;
  } catch (err) {
    toast.error(errorTitle, {
      description: errorMessage(err),
      duration: COPY_TOAST_ERROR_DURATION_MS,
      position: "bottom-right",
    });
    return false;
  }
}
