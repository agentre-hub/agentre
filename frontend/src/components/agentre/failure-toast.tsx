import type * as React from "react";
import { toast } from "sonner";

import { classifyAppError, type AppError } from "@/lib/app-error";

import { FailureBody } from "./failure-body";

/**
 * 失败 toast 的正文：后端带码的那句人话当正文；机器原话只进默认折叠的「详情」。
 * 两样都没有就不给正文。
 */
export function failureDescription(
  err: Pick<AppError, "message" | "detail">,
): React.ReactNode {
  if (!err.message && !err.detail) return undefined;
  return <FailureBody message={err.message} detail={err.detail} />;
}

/**
 * 「没有字段可贴」的失败（拖拽、排序、切换、安装…）统一这样说：标题是调用方给的
 * 本地化事实句，正文与详情按 `failureDescription`。`action` 是 toast 上的那颗键
 * （如「重试」）。各处不再各写一遍。
 */
export function toastFailure(
  title: string,
  cause: unknown,
  action?: { label: string; onClick: () => void },
): void {
  toast.error(title, {
    description: failureDescription(classifyAppError(cause)),
    ...(action ? { action } : {}),
  });
}

/**
 * 确认 / 改名这类小窗里的一次写入失败：窗留着，先说事实（`title`），后端带码的那句
 * 作正文，机器原话只进折叠的「详情」。
 */
export function FailureCallout({
  title,
  err,
}: {
  title: string;
  err: Pick<AppError, "message" | "detail">;
}) {
  return (
    <div
      role="alert"
      className="flex flex-col gap-1 rounded-md bg-destructive-soft px-3 py-2 text-2xs text-destructive-text"
    >
      <p className="font-medium">{title}</p>
      <FailureBody message={err.message} detail={err.detail} />
    </div>
  );
}
