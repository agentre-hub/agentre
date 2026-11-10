// 删除确认框里那次写入的失败（规格 2026-11-10 B1）：框留着，话说在页脚。

import { classifyAppError, errorText } from "@/lib/app-error";

export type DeleteFailure = {
  /** 页脚那句话：后端带码时是它按语言渲染好的业务文案，否则是调用方给的通用句。 */
  text: string;
  /** 机器原话，只进 `title`。 */
  raw: string;
};

export function deleteFailureOf(err: unknown, fallback: string): DeleteFailure {
  return {
    text: classifyAppError(err).message || fallback,
    raw: errorText(err).trim(),
  };
}

/** 页脚左侧那一行，顶替「不可恢复」的提示：点了删除的人视线就在这。 */
export function DeleteFailureLine({ failure }: { failure: DeleteFailure }) {
  return (
    <p
      role="alert"
      title={failure.raw || undefined}
      className="mr-auto min-w-0 truncate text-xs text-destructive-text"
    >
      {failure.text}
    </p>
  );
}
