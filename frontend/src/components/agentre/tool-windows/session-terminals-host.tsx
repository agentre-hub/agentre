import * as React from "react";
import { createPortal } from "react-dom";

import { TerminalTransportProvider } from "@agentre-hub/agentre-ui";
import {
  useSessionTerminalsStore,
  type SessionTerminal,
} from "@/stores/session-terminals-store";

import { detectBrowserPlatform } from "@/lib/platform";

import { lazyView } from "../lazy-boundary";
import { useOptionalShortcutsContext } from "../shortcuts/shortcuts-provider";
import { isToolWindowShortcutForTerminal } from "../shortcuts/tool-window-shortcuts";

import { createSessionTerminalTransport } from "./session-terminal-transport";

// xterm 整棵树只在出现终端时才拉；失败时由 lazyView 的边界显示重试。
const TerminalPanel = lazyView(
  () =>
    import("@agentre-hub/agentre-ui/terminal").then((m) => ({
      default: m.TerminalPanel,
    })),
  { overlay: true },
);

/**
 * 每个终端标签一个游离的 DOM 容器。终端面板（xterm + PTY 订阅）渲染进它，而容器只在
 * 终端工具窗显示时被挂进窗口里：隐藏窗口、切走会话都只是把容器摘下来，面板与进程
 * 原样留着——「隐藏终端窗不结束进程」靠的就是面板不随窗口卸载。
 */
const containers = new Map<string, HTMLDivElement>();

function keyOf(sessionId: number, terminalId: string): string {
  return `${sessionId}:${terminalId}`;
}

/** 取（必要时创建）一个终端标签的容器。窗口与 Host 都经它拿到同一个节点。 */
export function terminalContainer(
  sessionId: number,
  terminalId: string,
): HTMLDivElement {
  const key = keyOf(sessionId, terminalId);
  let el = containers.get(key);
  if (!el) {
    el = document.createElement("div");
    el.className = "flex min-h-0 min-w-0 flex-1 flex-col";
    containers.set(key, el);
  }
  return el;
}

/**
 * 常驻的会话终端宿主：为所有会话的所有终端标签渲染面板。它必须挂在对话标签页的
 * 外面（ChatPanelHost）——会话标签被替换 / 关闭时，别处的终端进程不该跟着结束；
 * 终端只在用户关标签（或进程自己退出）时才结束。
 */
export function SessionTerminalsHost() {
  const bySession = useSessionTerminalsStore((s) => s.bySession);

  // 标签没了就丢掉它的容器（面板此时已随标签卸载）。
  React.useEffect(() => {
    const live = new Set<string>();
    for (const [sid, entry] of Object.entries(bySession)) {
      for (const tab of entry.tabs) live.add(keyOf(Number(sid), tab.id));
    }
    for (const [key, el] of containers) {
      if (live.has(key)) continue;
      el.remove();
      containers.delete(key);
    }
  }, [bySession]);

  return (
    <>
      {Object.entries(bySession).flatMap(([sid, entry]) =>
        entry.tabs.map((tab) => (
          <HostedSessionTerminal
            key={keyOf(Number(sid), tab.id)}
            sessionId={Number(sid)}
            tab={tab}
            current={entry.activeId === tab.id}
          />
        )),
      )}
    </>
  );
}

const HostedSessionTerminal = React.memo(function HostedSessionTerminal({
  sessionId,
  tab,
  current,
}: {
  sessionId: number;
  tab: SessionTerminal;
  current: boolean;
}) {
  const viewing = useSessionTerminalsStore(
    (s) => s.viewing[sessionId] ?? false,
  );
  const close = useSessionTerminalsStore((s) => s.close);
  const markExited = useSessionTerminalsStore((s) => s.markExited);
  const openInCurrentRoot = useSessionTerminalsStore(
    (s) => s.openInCurrentRoot,
  );
  const platform =
    useOptionalShortcutsContext()?.platform ?? detectBrowserPlatform();
  // 焦点在终端里时，工具窗快捷键（⇧Esc、⌃数字…）不交给 xterm，照常冒到应用。
  const passThroughKey = React.useCallback(
    (ev: KeyboardEvent) => isToolWindowShortcutForTerminal(ev, platform),
    [platform],
  );

  const transport = React.useMemo(
    () =>
      createSessionTerminalTransport(sessionId, tab.root, (id) =>
        markExited(sessionId, id),
      ),
    [sessionId, tab.root, markExited],
  );
  const handleClose = React.useCallback(
    () => close(sessionId, tab.id),
    [close, sessionId, tab.id],
  );
  // 断线后「新开终端」：在本窗里按当前工作根新开一个标签；旧标签留着看输出，由用户关。
  const handleNewTerminal = React.useCallback(
    () => openInCurrentRoot(sessionId),
    [openInCurrentRoot, sessionId],
  );

  return createPortal(
    <TerminalTransportProvider transport={transport}>
      <TerminalPanel
        terminalID={tab.id}
        // 起始目录由会话传输按会话 + 根决定，这两个项目维度的入参用不上。
        projectId={0}
        deviceId=""
        active={current && viewing}
        attach={tab.attach}
        onClose={handleClose}
        onNewTerminal={handleNewTerminal}
        passThroughKey={passThroughKey}
      />
    </TerminalTransportProvider>,
    terminalContainer(sessionId, tab.id),
  );
});
