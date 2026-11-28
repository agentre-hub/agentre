import type { TerminalTransport } from "@agentre-hub/agentre-ui";
import * as App from "@/../wailsjs/go/app/App";

import { desktopTerminalTransport } from "../terminal/terminal-transport-desktop";

/**
 * 会话终端的传输：与桌面端的终端传输共用同一张订阅表与 write / resize / close，只在
 * 两处不同——
 *
 * - `open` 不按项目解析目录，而是让后端在**会话的某个已认领工作根**起 PTY
 *   （远端会话在执行设备上）。共享包里 `TerminalPanel` 的 open 入参只有项目与设备，
 *   这里在宿主这一侧改道，不动共享包；`root` 是起始根（主根 = 空串），按标签固定。
 * - `subscribe` 旁听退出：进程一退出就通知 `onExit`，让「有存活终端」的圆点熄灭
 *   （断线时标签还留着给用户看旧输出，不能靠标签是否存在来判断）。
 */
export function createSessionTerminalTransport(
  sessionId: number,
  root: string,
  onExit: (terminalId: string) => void,
): TerminalTransport {
  return {
    ...desktopTerminalTransport,
    subscribe(terminalId, subscriber) {
      return desktopTerminalTransport.subscribe(terminalId, {
        onData: (bytes) => subscriber.onData(bytes),
        onExit: (exit) => {
          onExit(terminalId);
          subscriber.onExit(exit);
        },
      });
    },
    async open({ terminalId, cols, rows }) {
      await App.TerminalOpenForSession(terminalId, sessionId, root, cols, rows);
    },
  };
}
