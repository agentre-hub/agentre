// 组织架构弹窗（新建、删除确认）里那次写入的失败：窗留着，话说在页脚。

import { cn } from "@agentre-hub/agentre-ui";

import { classifyAppError, errorText } from "@/lib/app-error";

export type WriteFailure = {
  /** 页脚那句话：后端带码时是它按语言渲染好的业务文案，否则是调用方给的通用句。 */
  text: string;
  /** 机器原话，只进 `title`。 */
  raw: string;
};

export function writeFailureOf(err: unknown, fallback: string): WriteFailure {
  return {
    text: classifyAppError(err).message || fallback,
    raw: errorText(err).trim(),
  };
}

/** 页脚左侧那一行：点了按钮的人视线就在这。 */
export function WriteFailureLine({
  failure,
  className,
}: {
  failure: WriteFailure;
  className?: string;
}) {
  return (
    <p
      role="alert"
      title={failure.raw || undefined}
      className={cn("truncate text-xs text-destructive-text", className)}
    >
      {failure.text}
    </p>
  );
}
