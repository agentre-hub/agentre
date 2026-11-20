import type { TFunction } from "i18next";
import { toast } from "sonner";

import { filePreviewFailureKind } from "../file-preview/ports";
import type { SessionLocation } from "./session-location";

const NOT_FOUND_REASON_KEY: Record<SessionLocation, string> = {
  remote: "richLink.maybeRemote",
  local: "richLink.maybeMovedOrDeleted",
};

/**
 * 转录里「交给系统应用打开」失败时的那一条提示，链接与图片降级 chip 共用。
 *
 * 本机没有这个文件（宿主 reject 带 `notFound` 标记）：说事实，不贴宿主的标记
 * token；原因按会话在哪台机器上给——远端会话说「可能在远端机器上」，本机会话说
 * 「可能已被移动或删除」，宿主没说就不猜。调用方给了 `preview` 时附上「预览」这条
 * 确定走得通的出口。其余失败沿用「打开失败: <宿主那句话>」。
 */
export function toastOpenPathFailure(
  err: unknown,
  t: TFunction,
  opts: { location?: SessionLocation; preview?: () => void } = {},
) {
  if (filePreviewFailureKind(err) === "notFound") {
    const { location, preview } = opts;
    toast.error(t("richLink.notOnThisMachine"), {
      ...(location ? { description: t(NOT_FOUND_REASON_KEY[location]) } : {}),
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
