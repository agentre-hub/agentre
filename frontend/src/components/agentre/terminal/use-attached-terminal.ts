import { useCallback, useEffect, useRef } from "react";
import type { Terminal } from "@xterm/xterm";
import { useTerminalTransport } from "@agentre-ai/agentre-ui";
import { useLocalCommandsStore } from "@/stores/local-commands-store";

// attach 模式数据源:不开 / 不关 PTY —— 那条 PTY 的所有权属于起它的那一方
// (本地命令卡片),这里只是搭个视图上去看。stdin / resize 走终端传输端口。
//
// 为什么输出仍从 useLocalCommandsStore 读增量,而不自己订阅端口:卡片那侧已经把
// 字节流按**一个** TextDecoder 解成了字符串,再开一路订阅就得再解一次 —— 而块
// 边界可能切在多字节字符中间,第二个解码器从半个字符处起手会吐 U+FFFD。共用卡片
// 那份已解码文本,既没有重复输出,也没有跨块乱码。
// (卡片的订阅由 launchLocalCommand 的闭包持有,不随宿主 Tab 卸载而中断,所以
// attach 视图在宿主 Tab 关掉之后依然跟得上。)
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
  const writtenLenRef = useRef(0);
  useEffect(() => {
    if (!enabled) return;
    writtenLenRef.current = 0;
    const writeDelta = () => {
      const entry = useLocalCommandsStore.getState().get(terminalID);
      const term = xtermRef.current;
      if (!entry || !term) return;
      if (entry.output.length > writtenLenRef.current) {
        term.write(entry.output.slice(writtenLenRef.current));
        writtenLenRef.current = entry.output.length;
      }
    };
    writeDelta(); // 首帧 seed:xterm 已由先于本 effect 跑的 layout effect 创建。
    const unsub = useLocalCommandsStore.subscribe(writeDelta);
    return () => unsub();
  }, [enabled, terminalID, xtermRef]);

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
