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

export async function copyTextWithToast(
  text: string,
  {
    errorTitle = i18n.t("common.copyFailed", { ns: AGENTRE_UI_NAMESPACE }),
    successDescription,
    successTitle,
  }: CopyTextWithToastOptions,
): Promise<boolean> {
  try {
    if (!navigator.clipboard?.writeText) {
      throw new Error(
        i18n.t("clipboard.unsupported", { ns: AGENTRE_UI_NAMESPACE }),
      );
    }
    await navigator.clipboard.writeText(text);
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
