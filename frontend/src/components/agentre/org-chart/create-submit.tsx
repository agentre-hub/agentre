// 两个新建对话框共用的提交态：提交中（转圈、不可关、不重复）与留在窗内的失败句。

import * as React from "react";

import { writeFailureOf, type WriteFailure } from "../org/write-failure-line";

/**
 * `run` 同一时刻只放行一次：busy 是状态，下一次渲染才生效，同一拍里的第二次提交
 * （连按回车）要靠 ref 挡住。失败不往外抛 —— 弹窗自己留着、把话说在页脚。
 */
export function useCreateSubmit(fallback: string) {
  const inFlight = React.useRef(false);
  const [busy, setBusy] = React.useState(false);
  const [failure, setFailure] = React.useState<WriteFailure | null>(null);

  const run = React.useCallback(
    async (submit: () => Promise<void>) => {
      if (inFlight.current) return;
      inFlight.current = true;
      setBusy(true);
      setFailure(null);
      try {
        await submit();
      } catch (err) {
        setFailure(writeFailureOf(err, fallback));
      } finally {
        inFlight.current = false;
        setBusy(false);
      }
    },
    [fallback],
  );

  return { busy, failure, run };
}
