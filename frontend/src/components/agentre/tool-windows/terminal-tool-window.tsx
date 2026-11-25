import * as React from "react";
import { Plus, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button, cn, pathBasename } from "@agentre-hub/agentre-ui";
import { useSessionRoot, type WorkRoot } from "@/stores/session-root-store";
import {
  useSessionTerminalsStore,
  type SessionTerminal,
} from "@/stores/session-terminals-store";

import { terminalContainer } from "./session-terminals-host";
import {
  ToolWindowTitleBarSlot,
  useToolWindowFrameKeys,
} from "./tool-window-frame";

type Props = {
  sessionId: number;
};

/**
 * 标签名：接入的进程用它的命令，其余是「终端 N」；会话有两个以上工作根时再带上这个
 * 终端起始根的根名，区分各个 worktree 里的终端（spec「终端工具窗」）。
 */
function useTerminalLabel(roots: readonly WorkRoot[]) {
  const { t } = useTranslation();
  return React.useCallback(
    (tab: SessionTerminal): string => {
      const title = tab.command
        ? t("chatTabs.terminal.titleWithCommand", { command: tab.command })
        : t("terminalToolWindow.tabTitle", { n: tab.seq });
      if (roots.length < 2 || tab.rootPath === "") return title;
      const name =
        roots.find((r) => r.path === tab.rootPath)?.name ??
        pathBasename(tab.rootPath);
      return t("terminalToolWindow.withRoot", { title, root: name });
    },
    [roots, t],
  );
}

/**
 * 「终端」工具窗：本会话的终端标签（spec「终端工具窗」）。
 *
 * 面板本身不在这里渲染——它们由常驻的 SessionTerminalsHost 持有，这里只把当前标签的
 * 容器挂进来。因此隐藏本窗、切换会话都不会结束终端进程。窗口刚显示、还没有终端时
 * 不替用户起进程（图标条的显示状态是持久化的，启动时恢复显示不该顺带开 PTY）。
 */
export function TerminalToolWindow({ sessionId }: Props) {
  const { t } = useTranslation();
  const entry = useSessionTerminalsStore((s) => s.bySession[sessionId]);
  const openInCurrentRoot = useSessionTerminalsStore(
    (s) => s.openInCurrentRoot,
  );
  const setActive = useSessionTerminalsStore((s) => s.setActive);
  const close = useSessionTerminalsStore((s) => s.close);
  const setViewing = useSessionTerminalsStore((s) => s.setViewing);
  const { roots } = useSessionRoot(sessionId);
  const labelOf = useTerminalLabel(roots);

  React.useLayoutEffect(() => {
    setViewing(sessionId, true);
    return () => setViewing(sessionId, false);
  }, [sessionId, setViewing]);

  const tabs = entry?.tabs ?? [];
  const activeId = entry?.activeId ?? null;
  const newTerminal = () => openInCurrentRoot(sessionId);

  if (tabs.length === 0) {
    return (
      <div className="flex min-h-0 flex-1 items-center justify-center">
        <Button type="button" variant="outline" size="sm" onClick={newTerminal}>
          <Plus aria-hidden="true" />
          {t("terminalToolWindow.new")}
        </Button>
      </div>
    );
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      {/* 标签在工具窗的标题栏里（spec「工具窗标题栏」）。 */}
      <ToolWindowTitleBarSlot>
        <div
          role="tablist"
          aria-label={t("terminalToolWindow.label")}
          className="flex h-7 min-w-0 flex-1 items-center gap-0.5 overflow-x-auto px-1"
        >
          {tabs.map((tab) => {
            const label = labelOf(tab);
            const selected = tab.id === activeId;
            return (
              <div
                key={tab.id}
                className={cn(
                  "group/terminal-tab flex h-6 shrink-0 items-center rounded-md text-xs",
                  selected
                    ? "bg-button-fill text-foreground"
                    : "text-muted-foreground hover:bg-rail-accent",
                  tab.exited && "opacity-60",
                )}
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  onClick={() => setActive(sessionId, tab.id)}
                  className="max-w-48 truncate pr-1 pl-2 outline-none focus-visible:focus-outline-inset"
                >
                  {label}
                </button>
                <button
                  type="button"
                  aria-label={t("terminalToolWindow.close", { name: label })}
                  onClick={() => close(sessionId, tab.id)}
                  className="mr-0.5 flex size-4 items-center justify-center rounded-sm hover:bg-accent"
                >
                  <X className="size-3" aria-hidden="true" />
                </button>
              </div>
            );
          })}
          <Button
            type="button"
            variant="ghost"
            size="icon-xs"
            aria-label={t("terminalToolWindow.new")}
            onClick={newTerminal}
          >
            <Plus aria-hidden="true" />
          </Button>
        </div>
      </ToolWindowTitleBarSlot>
      {activeId === null ? null : (
        <TerminalSlot
          key={`${sessionId}:${activeId}`}
          sessionId={sessionId}
          terminalId={activeId}
        />
      )}
    </div>
  );
}

/** 把当前标签的终端容器挂进窗口；卸载时只摘下来，面板与进程留在 Host 里。 */
function TerminalSlot({
  sessionId,
  terminalId,
}: {
  sessionId: number;
  terminalId: string;
}) {
  const ref = React.useRef<HTMLDivElement>(null);
  React.useLayoutEffect(() => {
    const slot = ref.current;
    if (!slot) return;
    const el = terminalContainer(sessionId, terminalId);
    slot.appendChild(el);
    return () => {
      if (el.parentNode === slot) slot.removeChild(el);
    };
  }, [sessionId, terminalId]);
  // 面板属于常驻宿主那棵 React 树，它的按键不经 React 冒到工具窗外壳：在这里用原生
  // 监听交给外壳（⇧Esc 隐藏、⇧⌘' 最大化）。
  const frameKeys = useToolWindowFrameKeys();
  React.useEffect(() => {
    const slot = ref.current;
    if (!slot || !frameKeys) return;
    slot.addEventListener("keydown", frameKeys);
    return () => slot.removeEventListener("keydown", frameKeys);
  }, [frameKeys]);
  return <div ref={ref} className="flex min-h-0 min-w-0 flex-1" />;
}
