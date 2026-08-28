import { useEffect, useState, useCallback } from "react";

import type { TerminalExit } from "./transport";
import { useTerminalTransport } from "./transport-context";

export type TerminalState = "opening" | "open" | "closing" | "idle";

export interface UseTerminalArgs {
  terminalID: string;
  projectId: number;
  deviceId: string;
  cols: number;
  rows: number;
  // enabled=false 时本 hook 完全 inert:不订阅 / 不 open,卸载也不 close。
  // attach 模式(接管别处已起的 PTY)用它把 live 路径关掉。默认启用,既有调用方不受影响。
  enabled?: boolean;
  onData?: (data: Uint8Array) => void;
  onExit?: (info: TerminalExit) => void;
}

export function useTerminal(args: UseTerminalArgs) {
  const [state, setState] = useState<TerminalState>("opening");
  const transport = useTerminalTransport();

  useEffect(() => {
    // attach 模式下完全不订阅 / 不开 PTY,清理也不 close —— 那条 PTY 的所有权
    // 属于起它的那一方(本地命令卡片),这里只是搭个视图上去看。
    if (args.enabled === false) return;
    let cancelled = false;

    // 先订阅再 open:PTY 可能在 open 兑现之前就吐出第一帧。
    const unsubscribe = transport.subscribe(args.terminalID, {
      onData: (bytes) => {
        args.onData?.(bytes);
      },
      onExit: (exit) => {
        args.onExit?.(exit);
        setState("idle");
      },
    });

    transport
      .open({
        terminalId: args.terminalID,
        projectId: args.projectId,
        deviceId: args.deviceId,
        cols: args.cols,
        rows: args.rows,
      })
      .then(
        () => {
          if (cancelled) {
            void transport.close(args.terminalID);
            return;
          }
          setState("open");
        },
        (err) => {
          if (!cancelled) {
            setState("idle");
            args.onExit?.({ code: -1, reason: "error", msg: String(err) });
          }
        },
      );

    return () => {
      cancelled = true;
      unsubscribe();
      transport.close(args.terminalID).catch(() => {});
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [args.terminalID]);

  const write = useCallback(
    (data: string) => transport.write(args.terminalID, data),
    [transport, args.terminalID],
  );

  const resize = useCallback(
    (cols: number, rows: number) =>
      transport.resize(args.terminalID, cols, rows),
    [transport, args.terminalID],
  );

  return { state, write, resize };
}
