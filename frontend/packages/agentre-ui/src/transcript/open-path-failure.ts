import type { TFunction } from "i18next";
import { toast } from "sonner";

import { filePreviewFailureKind } from "../file-preview/ports";

/**
 * 转录里「交给系统应用打开」失败时的那一条提示，链接与图片降级 chip 共用。
 *
 * 本机没有这个文件（远端会话的文件在另一台机器上，宿主 reject 带 `notFound`
 * 标记）：说事实，不贴宿主的标记 token；调用方给了 `preview` 时附上「预览」这条
 * 确定走得通的出口。其余失败沿用「打开失败: <宿主那句话>」。
 */
export function toastOpenPathFailure(
  err: unknown,
  t: TFunction,
  preview?: () => void,
) {
  if (filePreviewFailureKind(err) === "notFound") {
    toast.error(t("richLink.notOnThisMachine"), {
      description: t("richLink.maybeRemote"),
      ...(preview
        ? { action: { label: t("richLink.openPreview"), onClick: preview } }
        : {}),
    });
    return;
  }
  toast.error(
    t("richLink.openFailed", {
      error: err instanceof Error ? err.message : String(err),
    }),
  );
}
