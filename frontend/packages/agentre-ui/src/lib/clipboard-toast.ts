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
 */
function copyViaExecCommand(text: string): boolean {
  if (typeof document.execCommand !== "function") return false;

  const textarea = document.createElement("textarea");
  textarea.value = text;
  // 不能用 display:none / hidden：不在渲染树里的元素选不中，execCommand 会空手而归。
  // 挪出视口 + readonly 才能既看不见、又不在移动端弹起键盘。
  textarea.setAttribute("readonly", "");
  textarea.style.position = "fixed";
  textarea.style.top = "-9999px";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);

  try {
    textarea.focus();
    textarea.select();
    return document.execCommand("copy");
  } catch {
    return false;
  } finally {
    textarea.remove();
  }
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
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
    } else if (!copyViaExecCommand(text)) {
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
