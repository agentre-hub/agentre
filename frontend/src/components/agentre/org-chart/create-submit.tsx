// 两个新建对话框共用的提交态：提交中（转圈、不可关、不重复）与留在窗内的失败句。

import * as React from "react";

import { classifyAppError, errorText } from "@/lib/app-error";

export type CreateFailure = {
  /** 页脚那句话：后端带码时是它按语言渲染好的业务文案，否则是调用方给的通用句。 */
  text: string;
  /** 机器原话，只进 `title`。 */
  raw: string;
};

/**
 * `run` 同一时刻只放行一次：busy 是状态，下一次渲染才生效，同一拍里的第二次提交
 * （连按回车）要靠 ref 挡住。失败不往外抛 —— 弹窗自己留着、把话说在页脚。
 */
export function useCreateSubmit(fallback: string) {
  const inFlight = React.useRef(false);
  const [busy, setBusy] = React.useState(false);
  const [failure, setFailure] = React.useState<CreateFailure | null>(null);

  const run = React.useCallback(
    async (submit: () => Promise<void>) => {
      if (inFlight.current) return;
      inFlight.current = true;
      setBusy(true);
      setFailure(null);
      try {
        await submit();
      } catch (err) {
        const { message } = classifyAppError(err);
        setFailure({ text: message || fallback, raw: errorText(err).trim() });
      } finally {
        inFlight.current = false;
        setBusy(false);
      }
    },
    [fallback],
  );

  return { busy, failure, run };
}

/** 页脚左侧那一行：外壳的 `error` 只收字符串、带不了 `title`，所以走 `left`。 */
export function CreateFailureLine({ failure }: { failure: CreateFailure }) {
  return (
    <p
      role="alert"
      title={failure.raw || undefined}
      className="truncate text-xs text-destructive-text"
    >
      {failure.text}
    </p>
  );
}
