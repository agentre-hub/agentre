import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";

import { useUiTranslation } from "../../i18n";
import {
  computeTerminalHeight,
  FALLBACK_CELL_PX,
  MAX_ROWS,
  MIN_ROWS,
  PADDING_PX,
} from "../../terminal/terminal-height";
import {
  readTerminalTheme,
  TERMINAL_FONT_FAMILY,
} from "../../terminal/terminal-theme";

import { useLocalCommand, useLocalCommandsAccess } from "./access";

// 本地命令(`!cmd`)输出的只读展示。复用交互终端同一套 xterm 渲染:让 xterm 自己解释
// ANSI/OSC/控制序列(颜色、光标、标题序列…),而不是用正则剥转义 —— 后者会漏掉前导
// ESC 字节、OSC、裸 \r 等,在 <pre> 里渲染成乱码。输出经宿主接缝的 subscribeOutput
// 增量拿到(见 access.tsx):这条路刻意不反应式 —— 每个 chunk 触发一次 React 重渲染
// 会毁掉长跑命令的性能,而字节的去处是 xterm 的 write(),本来就不需要经过 React。
//
// 只读:disableStdin + 不闪光标,不接 stdin、不 resize PTY(命令可能已结束,PTY 已退)。
// 懒挂载:卡片滚入视口才建 xterm —— 长 transcript 里每张卡都常驻一个 canvas 终端太重。
// 环境无 IntersectionObserver(happy-dom 测试 / SSR)时直接 eager,保证输出始终可见。
export function OutputTerminal({ terminalId }: { terminalId: string }) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const xtermRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const [mounted, setMounted] = useState(false);
  const { t } = useUiTranslation();
  const cellHeightRef = useRef(FALLBACK_CELL_PX);
  const commands = useLocalCommandsAccess();
  const entry = useLocalCommand(terminalId);
  // 反应式读只看「跑完了没有」与「有没有过输出」—— 两者都只翻一次,不随 chunk 变。
  const isEmptyFinished =
    !!entry && entry.status !== "running" && !entry.hasOutput;
  // 组合门控:懒挂载 && 未落到「空输出已结束」占位分支。一个静默、长跑的命令可能
  // 在运行中(尚无输出)就滚入视口挂载了真实 xterm;结束时若输出仍是空串,组件会
  // 切到占位分支 —— 但 mounted 这个一次性锁存不会回落,若仍用它做依赖,四个
  // xterm 生命周期 effect 都不会重跑清理,Terminal/两个 Observer/输出订阅就此泄漏。
  // 用 active 替代 mounted 让 isEmptyFinished 翻真时也能触发一次 cleanup。
  const active = mounted && !isEmptyFinished;

  // 懒挂载门控。
  useEffect(() => {
    const el = containerRef.current;
    if (!el) return;
    if (typeof IntersectionObserver === "undefined") {
      setMounted(true);
      return;
    }
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) {
        setMounted(true);
        io.disconnect();
      }
    });
    io.observe(el);
    return () => io.disconnect();
  }, []);

  // 构建只读 xterm(layout effect:在写入前先建好实例)。
  useLayoutEffect(() => {
    if (!active || !containerRef.current) return;
    const term = new Terminal({
      fontFamily: TERMINAL_FONT_FAMILY,
      fontSize: 13,
      theme: readTerminalTheme(),
      scrollback: 1000,
      disableStdin: true,
      cursorBlink: false,
      cursorStyle: "bar",
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.open(containerRef.current);
    fit.fit();
    xtermRef.current = term;
    fitRef.current = fit;
    return () => {
      term.dispose();
      xtermRef.current = null;
      fitRef.current = null;
    };
  }, [active]);

  // seed + 增量:写新增片段 + 每次按内容重算容器高度(自适应且封顶)。
  // 订阅自带游标,重新挂载(active 翻真)时会重新从头 seed 进新建的那个 xterm。
  useEffect(() => {
    if (!active) return;
    const applyHeight = () => {
      const term = xtermRef.current;
      const el = containerRef.current;
      if (!term || !el) return;
      // 有真实布局时用它反推行高(容器高 - padding)/ 行数;happy-dom 下退回兜底常量。
      if (el.clientHeight > 0 && term.rows > 0) {
        const m = (el.clientHeight - PADDING_PX) / term.rows;
        if (Number.isFinite(m) && m > 0) cellHeightRef.current = m;
      }
      const b = term.buffer.active;
      const contentRows = b.baseY + b.cursorY + 1;
      el.style.height = `${computeTerminalHeight({
        contentRows,
        cellHeight: cellHeightRef.current,
        minRows: MIN_ROWS,
        maxRows: MAX_ROWS,
        paddingPx: PADDING_PX,
      })}px`;
      fitRef.current?.fit();
    };
    const unsubscribe = commands.subscribeOutput(terminalId, (chunk) => {
      xtermRef.current?.write(chunk);
      applyHeight();
    });
    applyHeight(); // 还没有任何输出时也要定高,别留一个 0 高的空盒子。
    return () => unsubscribe();
  }, [active, commands, terminalId]);

  // 容器宽度变化时重新 fit(只换列宽以正确回绕,不 resize PTY)。
  useEffect(() => {
    if (!active || !containerRef.current) return;
    const ro = new ResizeObserver(() => fitRef.current?.fit());
    ro.observe(containerRef.current);
    return () => ro.disconnect();
  }, [active]);

  // 跟随 app light/dark 切换重置终端主题(与交互终端一致)。
  useEffect(() => {
    if (!active || typeof document === "undefined") return;
    const apply = () => {
      const term = xtermRef.current;
      if (term) term.options.theme = readTerminalTheme();
    };
    apply();
    const obs = new MutationObserver(apply);
    obs.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => obs.disconnect();
  }, [active]);

  if (isEmptyFinished) {
    return (
      <div
        data-testid="local-command-terminal"
        className="bg-code-surface px-3 py-2 text-2xs text-muted-foreground"
      >
        {t("localCommand.noOutput")}
      </div>
    );
  }

  return (
    <div
      ref={containerRef}
      data-testid="local-command-terminal"
      className="w-full overflow-hidden bg-code-surface px-2 py-1.5"
    />
  );
}
