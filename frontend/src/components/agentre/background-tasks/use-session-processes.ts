import * as React from "react";

import { ListSessionProcesses } from "../../../../wailsjs/go/app/App";
import type { SessionProcess } from "./types";

const POLL_MS = 5000;

// 会话留下的存活进程。active(面板看得见且有会话)时立刻拉一次、之后每 5 秒一次;
// 失败按「没有进程」处理,不暴露错误态。refresh 给宿主在弹层打开 / 结束进程后立刻补拉。
export function useSessionProcesses(
  sessionId: number | undefined,
  active: boolean,
): { processes: SessionProcess[]; refresh: () => Promise<void> } {
  const [processes, setProcesses] = React.useState<SessionProcess[]>([]);
  // 只认最新一次请求的结果:会话切换 / 手动 refresh 后,更早发出的响应作废。
  const seqRef = React.useRef(0);
  const inFlightRef = React.useRef(false);
  const enabled = active && sessionId != null && sessionId > 0;

  const fetchOnce = React.useCallback(async () => {
    if (!enabled || sessionId == null) return;
    const seq = ++seqRef.current;
    inFlightRef.current = true;
    let next: SessionProcess[];
    try {
      const resp = await ListSessionProcesses(sessionId);
      next = (resp?.processes ?? []).map((p) => ({
        pid: p.pid,
        command: p.command,
        ports: p.ports ?? [],
        startedAt: p.startedAt,
        cwd: p.cwd || undefined,
      }));
    } catch {
      next = [];
    }
    if (seq !== seqRef.current) return;
    inFlightRef.current = false;
    setProcesses(next);
  }, [enabled, sessionId]);

  React.useEffect(() => {
    if (!enabled) {
      seqRef.current++; // 作废在途请求
      inFlightRef.current = false;
      setProcesses([]);
      return;
    }
    void fetchOnce();
    const timer = setInterval(() => {
      if (!inFlightRef.current) void fetchOnce();
    }, POLL_MS);
    const seq = seqRef;
    const inFlight = inFlightRef;
    return () => {
      clearInterval(timer);
      seq.current++;
      inFlight.current = false;
      // 换会话时旧会话的行立刻作废:否则新列表回来之前,「结束 / 打开」会带着新会话
      // id 去操作旧会话的进程(远程会话还会在错的设备上建端口转发)。
      setProcesses([]);
    };
  }, [enabled, fetchOnce]);

  return { processes, refresh: fetchOnce };
}
