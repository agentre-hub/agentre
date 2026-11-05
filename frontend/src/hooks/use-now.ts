import * as React from "react";

/**
 * 每隔 intervalMs 刷新一次的当前时间，让「N 分钟前」这类相对时间自己走。渲染期间
 * 不直接调 `Date.now()`（react-hooks/purity），读这一份。
 */
export function useNow(intervalMs: number): number {
  const [now, setNow] = React.useState(() => Date.now());
  React.useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), intervalMs);
    return () => window.clearInterval(id);
  }, [intervalMs]);
  return now;
}
