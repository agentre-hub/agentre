import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { Terminal } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { WebLinksAddon } from "@xterm/addon-web-links";
import "@xterm/xterm/css/xterm.css";
import { toast } from "sonner";

import { useUiTranslation } from "../i18n";

import { TERMINAL_FONT_FAMILY, readTerminalTheme } from "./terminal-theme";
import { useTerminal } from "./use-terminal";
import { useAttachedTerminal } from "./use-attached-terminal";
import { attachXtermRolloverGuard } from "./xterm-rollover-guard";

export interface TerminalPanelProps {
  terminalID: string;
  projectId: number;
  deviceId: string;
  active?: boolean;
  // attach=true:接管别处已起的 PTY(本地命令卡片),从 store seed + 增量渲染,
  // 不开 / 不关 PTY。默认 false 走既有 live 路径。
  attach?: boolean;
  onClose: () => void;
}

export function TerminalPanel({
  terminalID,
  projectId,
  deviceId,
  active = true,
  attach = false,
  onClose,
}: TerminalPanelProps) {
  const { t } = useUiTranslation();
  const containerRef = useRef<HTMLDivElement | null>(null);
  const xtermRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const [connectionLost, setConnectionLost] = useState(false);

  // Stable dismiss for the banner — calls onClose, which unmounts us.
  const dismissAndClose = useCallback(() => {
    setConnectionLost(false);
    onClose();
  }, [onClose]);

  const handleExit = useCallback(
    (info: { code: number; reason: string; msg?: string }) => {
      if (info.reason === "connection_lost") {
        setConnectionLost(true);
        toast.error(t("terminal.toast.connectionLost"));
        return; // banner is shown; user dismisses to close.
      }
      if (info.reason === "error") {
        toast.error(
          t("terminal.toast.startFailed", {
            message: info.msg ?? t("terminal.unknown"),
          }),
        );
        onClose();
        return;
      }
      if (info.reason === "daemon_shutdown") {
        toast.warning(t("terminal.toast.agentredClosed"));
        onClose();
        return;
      }
      if (info.reason === "natural" && info.code !== 0) {
        toast.warning(t("terminal.toast.shellExited", { code: info.code }));
      }
      // natural code=0 or killed → silent close.
      onClose();
    },
    [onClose, t],
  );

  // 两个数据源 hook 都无条件调用(满足 hooks 规则),各自被 enabled 关掉一个;
  // attach 模式接管已起 PTY,live 模式开新 PTY。
  const live = useTerminal({
    terminalID,
    projectId,
    deviceId,
    cols: 80,
    rows: 24,
    enabled: !attach,
    onData: (data) => {
      xtermRef.current?.write(data);
    },
    onExit: handleExit,
  });
  const attached = useAttachedTerminal({
    terminalID,
    xtermRef,
    enabled: attach,
  });
  const { state, write, resize } = attach ? attached : live;

  const fitAndResize = useCallback(() => {
    const f = fitRef.current;
    const t = xtermRef.current;
    if (!f || !t) return;
    f.fit();
    void resize(t.cols, t.rows);
  }, [resize]);

  useLayoutEffect(() => {
    if (!containerRef.current) return;
    const term = new Terminal({
      fontFamily: TERMINAL_FONT_FAMILY,
      fontSize: 13,
      theme: readTerminalTheme(),
      scrollback: 500,
      cursorBlink: true,
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.loadAddon(new WebLinksAddon());
    term.open(containerRef.current);

    // Cmd+C/Ctrl+C: copy selection if any, else fall through to xterm SIGINT default.
    term.attachCustomKeyEventHandler((ev) => {
      // 在 IME composition / keyCode=229 时早返回，别吞掉应交给 IME / xterm 内部
      // 处理的按键，否则可能导致字符丢失 (W3C UI Events §5.4.3)。
      if (ev.isComposing || ev.keyCode === 229) return true;
      if (ev.type !== "keydown") return true;
      const isCopyCombo =
        (ev.ctrlKey || ev.metaKey) &&
        !ev.shiftKey &&
        !ev.altKey &&
        (ev.key === "c" || ev.key === "C");
      if (!isCopyCombo) return true;
      const selection = term.getSelection();
      if (!selection) return true; // no selection → let xterm send SIGINT
      // Has selection → copy + swallow event so SIGINT does not fire.
      void navigator.clipboard.writeText(selection).catch(() => {
        // Clipboard permission may be blocked; intentionally silent.
      });
      return false;
    });

    xtermRef.current = term;
    fitRef.current = fit;

    return () => {
      term.dispose();
      xtermRef.current = null;
      fitRef.current = null;
    };
  }, []);

  useLayoutEffect(() => {
    if (!active) return;
    xtermRef.current?.focus();
    if (state === "open") {
      fitAndResize();
    }
  }, [active, state, fitAndResize]);

  // Re-theme xterm when the app switches between light and dark mode.
  useEffect(() => {
    if (typeof document === "undefined") return;
    const applyTheme = () => {
      const term = xtermRef.current;
      if (!term) return;
      term.options.theme = readTerminalTheme();
    };
    // Apply once on mount. App toggles the `.dark` class in its own layout
    // effect; on a same-commit mount (a restored terminal tab on app startup)
    // that runs AFTER this terminal's xterm was constructed but BEFORE this
    // observer registers, so the MutationObserver never sees that initial class
    // and the terminal would stay light. This passive effect runs after all
    // layout effects, so re-reading here picks up the resolved theme.
    applyTheme();
    const observer = new MutationObserver(applyTheme);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    const term = xtermRef.current;
    if (!term) return;
    const send = (d: string) => void write(d);
    const sub = term.onData(send);
    // IME 快速输入丢字符旁路：xterm 在 key-rollover 时会把中间字符当重复输入丢掉，
    // 这里在 textarea 的 input 事件上补发被丢的字符 (详见 xterm-rollover-guard.ts)。
    const guard = attachXtermRolloverGuard(term, send);
    return () => {
      sub.dispose();
      guard.dispose();
    };
  }, [write]);

  useEffect(() => {
    if (!containerRef.current) return;
    const ro = new ResizeObserver(() => {
      if (!active || state !== "open") return;
      fitAndResize();
    });
    ro.observe(containerRef.current);
    return () => ro.disconnect();
  }, [active, state, fitAndResize]);

  return (
    <div
      className="flex flex-1 min-h-0 flex-col bg-background"
      data-testid="terminal-panel"
    >
      {connectionLost ? (
        <div
          role="alert"
          className="flex items-center justify-between border-b border-status-error/40 bg-destructive-soft px-3 py-2 text-xs text-destructive"
        >
          <span>{t("terminal.banner.connectionLost")}</span>
          <button
            type="button"
            onClick={dismissAndClose}
            className="rounded border border-status-error/40 px-2 py-0.5 hover:bg-destructive/10"
          >
            {t("common.close")}
          </button>
        </div>
      ) : null}
      <div ref={containerRef} className="h-full w-full p-2" />
    </div>
  );
}
