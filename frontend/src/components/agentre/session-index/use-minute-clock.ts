// frontend/src/components/agentre/session-index/use-minute-clock.ts
//
// 会话索引行尾的相对时间（「2m」「3h」）按分钟走。行对象按会话缓存、只在自己的 meta /
// 状态变化时重算，没有这一拍的话，一条不动的会话就一直停在算出来的那一刻。所有订阅方
// 共用一个定时器：有人订阅才开，最后一个离开就停。
import * as React from "react";

const MINUTE_MS = 60_000;

let minute = Math.floor(Date.now() / MINUTE_MS);
let timer: ReturnType<typeof setInterval> | null = null;
const listeners = new Set<() => void>();

function tick(): void {
  const next = Math.floor(Date.now() / MINUTE_MS);
  if (next === minute) return;
  minute = next;
  for (const listener of listeners) listener();
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener);
  if (timer === null) {
    minute = Math.floor(Date.now() / MINUTE_MS);
    timer = setInterval(tick, MINUTE_MS / 4);
  }
  return () => {
    listeners.delete(listener);
    if (listeners.size === 0 && timer !== null) {
      clearInterval(timer);
      timer = null;
    }
  };
}

function getSnapshot(): number {
  return minute;
}

/** useMinuteClock 交回当前的分钟序号；每跨过一分钟变一次。 */
export function useMinuteClock(): number {
  return React.useSyncExternalStore(subscribe, getSnapshot, getSnapshot);
}
