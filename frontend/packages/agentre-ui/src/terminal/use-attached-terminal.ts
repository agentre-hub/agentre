import { useCallback, useEffect } from "react";
import type { Terminal } from "@xterm/xterm";

import { useOptionalLocalCommandsAccess } from "../transcript/local-command/access";

import { useTerminalTransport } from "./transport-context";

// attach 模式数据源:不开 / 不关 PTY —— 那条 PTY 的所有权属于起它的那一方
// (本地命令卡片),这里只是搭个视图上去看。stdin / resize 走终端传输端口。
//
// 为什么输出不自己订阅传输端口,而走本地命令接缝的 subscribeOutput:卡片那侧已经把
// 字节流按**一个** TextDecoder 解成了字符串,再开一路订阅就得再解一次 —— 而块
// 边界可能切在多字节字符中间,第二个解码器从半个字符处起手会吐 U+FFFD。共用卡片
// 那份已解码文本,既没有重复输出,也没有跨块乱码。
// (宿主对那条 PTY 的订阅由发起命令的那一方持有,不随 Tab 卸载而中断,所以 attach
// 视图在标签关掉之后依然跟得上。)
//
// 这也是包内唯一一处 terminal → transcript 的方向依赖:attach 模式存在的理由就是
// 「把一张本地命令卡片挂进终端标签」,两侧盯的是同一条命令,不是巧合的耦合。
export function useAttachedTerminal({
  terminalID,
  xtermRef,
  enabled,
}: {
  terminalID: string;
  xtermRef: React.RefObject<Terminal | null>;
  enabled: boolean;
}) {
  const transport = useTerminalTransport();
  // 可选取用:TerminalPanel 无条件调用本 hook(hooks 规则),但 live 模式那条 PTY
  // 与本地命令无关。用会炸的取用口会让「没有本地命令能力的宿主开不了普通终端」,
  // 把 attach 模式的依赖摊派给所有终端。缺接线只在真的 attach 时才是错误。
  const commands = useOptionalLocalCommandsAccess();
  useEffect(() => {
    if (!enabled) return;
    if (!commands) {
      // 英文文案：本仓库约定注释可中文、生产代码的字符串字面量一律英文。
      throw new Error(
        "useAttachedTerminal in attach mode requires a <LocalCommandsProvider>",
      );
    }
    // 订阅自带游标:挂上时同步 seed 一次积压输出(xterm 已由先于本 effect 跑的
    // layout effect 创建),此后只收增量。
    const unsubscribe = commands.subscribeOutput(terminalID, (chunk) => {
      xtermRef.current?.write(chunk);
    });
    return () => unsubscribe();
  }, [commands, enabled, terminalID, xtermRef]);

  const write = useCallback(
    (d: string) => transport.write(terminalID, d),
    [transport, terminalID],
  );
  const resize = useCallback(
    (c: number, r: number) => transport.resize(terminalID, c, r),
    [transport, terminalID],
  );
  // state 恒为 "open":PTY 已在跑,TerminalPanel 的 fit/resize effect(gated on
  // state==="open")才会执行。
  return { state: "open" as const, write, resize };
}
