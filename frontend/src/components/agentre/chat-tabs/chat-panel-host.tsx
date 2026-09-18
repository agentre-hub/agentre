// frontend/src/components/agentre/chat-tabs/chat-panel-host.tsx
import * as React from "react";
import { Check, Sparkles } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import {
  Badge,
  Button,
  TerminalPanel,
  cn,
  pruneChatPanelScrollState,
} from "@agentre-hub/agentre-ui";

import { ChatPanel } from "../chat-panel";
import { PeerPanel } from "../peer/peer-panel";
import { formatChord, formatPrimaryModifier } from "../shortcuts/format";
import { TAB_CHIP_IDS, TAB_CLOSE_ID } from "../shortcuts/registry";
import { useOptionalShortcutsContext } from "../shortcuts/shortcuts-provider";
import type { KeyChord } from "../shortcuts/types";
import { detectBrowserPlatform } from "@/lib/platform";
import { reloadSidebarSources } from "@/stores/sidebar-reload";
import type { ChatTab, TabKind } from "@/stores/chat-tabs-store";
import { useChatTabsStore } from "@/stores/chat-tabs-store";
import { useChatAgentsStore } from "@/stores/chat-agents-store";

type PanelOrderState = {
  tabs: ChatTab[];
  order: string[];
};

function reconcilePanelOrder(order: string[], tabs: ChatTab[]) {
  const liveIds = new Set(tabs.map((tab) => tab.id));
  const next = order.filter((id) => liveIds.has(id));
  const knownIds = new Set(next);

  for (const tab of tabs) {
    if (knownIds.has(tab.id)) continue;
    next.push(tab.id);
    knownIds.add(tab.id);
  }

  return next;
}

function tabsByPanelOrder(tabs: ChatTab[], order: string[]) {
  const byId = new Map(tabs.map((tab) => [tab.id, tab]));
  return order
    .map((id) => byId.get(id))
    .filter((tab): tab is ChatTab => Boolean(tab));
}

export function ChatPanelHost() {
  const tabs = useChatTabsStore((s) => s.tabs);
  const activeTabId = useChatTabsStore((s) => s.activeTabId);
  const [panelOrderState, setPanelOrderState] = React.useState<PanelOrderState>(
    () => ({
      tabs,
      order: reconcilePanelOrder([], tabs),
    }),
  );

  let panelOrder = panelOrderState.order;
  if (panelOrderState.tabs !== tabs) {
    panelOrder = reconcilePanelOrder(panelOrderState.order, tabs);
    setPanelOrderState({ tabs, order: panelOrder });
  }

  const panelTabs = React.useMemo(
    () => tabsByPanelOrder(tabs, panelOrder),
    [panelOrder, tabs],
  );
  React.useEffect(() => {
    pruneChatPanelScrollState(new Set(tabs.map((tab) => tab.id)));
  }, [tabs]);

  if (tabs.length === 0) {
    return <ChatEmptyState />;
  }

  return (
    <div className="relative flex h-full min-h-0 flex-1 flex-col overflow-hidden">
      {panelTabs.map((t) =>
        t.meta.kind === "terminal" ? (
          <HostedTerminalPanel
            key={t.id}
            tab={t}
            active={t.id === activeTabId}
          />
        ) : t.meta.kind === "peer" ? (
          <HostedPeerPanel key={t.id} tab={t} active={t.id === activeTabId} />
        ) : (
          <HostedPanel key={t.id} tab={t} active={t.id === activeTabId} />
        ),
      )}
    </div>
  );
}

// 空聊天态分两档 (spec §7):
//   - 1B: 没有任何可对话 Agent → 两步配置引导 (配置 Agent 后端 / LLM 供应商, 各带跳转按钮)
//   - 1C: 已有可对话 Agent → 保留原占位, 底部补一行「N 个 Agent 未配置后端」徽标 + 去组织架构链接
function ChatEmptyState() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const agents = useChatAgentsStore((s) => s.agents);
  const loading = useChatAgentsStore((s) => s.loading);
  const error = useChatAgentsStore((s) => s.error);

  // ChatPanelHost 在非 /chat 路由 (projects / settings / org) 也保持挂载,
  // 此时 sidebar (ChatPage) 未挂载、不会帮我们拉 agents —— 空态自己补一次
  // reload (store 内并发去重, 已 in-flight 时复用), 保证两档判定数据新鲜。
  React.useEffect(() => {
    void useChatAgentsStore.getState().reload();
  }, []);

  const hasChattable = agents.some((a) => a.chattable);
  const unconfiguredCount = agents.filter(
    (a) => a.blockReason === "no-backend",
  ).length;

  if (!loading && !error && !hasChattable) {
    return (
      <main className="flex flex-1 flex-col items-center justify-center gap-3 overflow-auto bg-background px-8 py-10 text-center">
        <span className="inline-flex size-14 items-center justify-center rounded-lg border border-border bg-primary-soft">
          <Sparkles className="size-6 text-primary" aria-hidden="true" />
        </span>
        <SetupChecklist />
        <ChatShortcuts />
      </main>
    );
  }

  return (
    <main className="flex flex-1 flex-col items-center justify-center gap-3 bg-background px-8 text-center">
      <span className="inline-flex size-14 items-center justify-center rounded-lg border border-border bg-primary-soft">
        <Sparkles className="size-6 text-primary" aria-hidden="true" />
      </span>
      <div className="text-base font-semibold">{t("chatTabs.empty.title")}</div>
      <div className="text-xs text-muted-foreground">
        {t("chatTabs.empty.description")}
      </div>
      {unconfiguredCount > 0 ? (
        <div className="mt-3 flex items-center gap-2 text-xs">
          <Badge
            variant="outline"
            className="border-status-waiting/40 bg-status-waiting-bg px-2 py-0 text-2xs text-foreground"
          >
            {t("chatTabs.empty.unconfigured.count", {
              count: unconfiguredCount,
            })}
          </Badge>
          <Button
            type="button"
            variant="link"
            className="h-auto px-0 text-xs"
            onClick={() => navigate("/org")}
          >
            {t("chatTabs.empty.unconfigured.goOrg")}
          </Button>
        </div>
      ) : null}
      <ChatShortcuts />
    </main>
  );
}

function ChatShortcuts() {
  const { t } = useTranslation();
  // kbd 文案按平台生成：有 Provider 就跟它的 platform，没有（极少数测试场景）
  // 才退回浏览器探测。绑定优先读用户重绑后的值，没有才用注册表默认值。
  const shortcuts = useOptionalShortcutsContext();
  const platform = shortcuts?.platform ?? detectBrowserPlatform();
  const chordFor = (id: string, fallback: KeyChord): KeyChord =>
    shortcuts?.bindings.get(id) ?? fallback;

  const firstTabChord = chordFor(TAB_CHIP_IDS[0], {
    mod: "primary",
    key: "1",
  });
  const lastTabChord = chordFor(TAB_CHIP_IDS[TAB_CHIP_IDS.length - 1], {
    mod: "primary",
    key: String(TAB_CHIP_IDS.length),
  });
  // macOS 习惯在区间两端都写出修饰键（⌘1..⌘9）；其它平台只在开头写一次
  // （Ctrl+1..9）。
  const firstTabLabel = formatChord(firstTabChord, platform);
  const lastTabLabel = formatChord(lastTabChord, platform);
  const tabsLabel =
    platform === "darwin"
      ? `${firstTabLabel}..${lastTabLabel}`
      : `${firstTabLabel}..${lastTabChord.key}`;
  const closeLabel = formatChord(
    chordFor(TAB_CLOSE_ID, { mod: "primary", key: "W" }),
    platform,
  );
  // 「Click」是词不是键：macOS 沿用原样的空格（⌘ Click），其它平台与
  // formatChord 一致用「+」（Ctrl+Click）。
  const clickLabel = `${formatPrimaryModifier(platform)}${
    platform === "darwin" ? " " : "+"
  }Click`;

  return (
    <div className="mt-2 flex items-center gap-3 text-xs text-muted-foreground">
      <kbd className="rounded-md border border-border bg-card px-2 py-1 font-mono">
        {tabsLabel}
      </kbd>
      {t("chatTabs.empty.shortcuts.switch")}
      <kbd className="rounded-md border border-border bg-card px-2 py-1 font-mono">
        {closeLabel}
      </kbd>
      {t("chatTabs.empty.shortcuts.close")}
      <kbd className="rounded-md border border-border bg-card px-2 py-1 font-mono">
        {clickLabel}
      </kbd>
      {t("chatTabs.empty.shortcuts.openInNewTab")}
    </div>
  );
}

type SetupStepStatus = "done" | "current" | "waiting";

// 1B 引导清单：三行按 store 的真实状态排出「已完成 / 当前该做 / 等待前置」。
// 后端完成 ⇔ 有 agent 的 blockReason 不是 no-backend；provider 完成 ⇔ 有 chattable
// 的 agent；第一步与第二步都完成，第三行（发出第一轮对话）才可点。
function SetupChecklist() {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const agents = useChatAgentsStore((s) => s.agents);

  const backendDone = agents.some((a) => a.blockReason !== "no-backend");
  const providerDone = agents.some((a) => a.chattable);
  const canStart = backendDone && providerDone;

  const goBackend = () =>
    navigate("/settings", { state: { settingsPage: "agent-backend" } });
  const goProvider = () =>
    navigate("/settings", { state: { settingsPage: "llm-providers" } });
  const startFirstChat = () => {
    const agent = agents.find((a) => a.chattable);
    if (agent) useChatTabsStore.getState().openNewSession(0, agent.id, "");
  };

  const steps: Array<{
    id: string;
    index: number;
    title: string;
    description: string;
    status: SetupStepStatus;
    actionLabel: string;
    onAction: () => void;
  }> = [
    {
      id: "backend",
      index: 1,
      title: t("chatTabs.empty.setupGuide.backend.title"),
      description: backendDone
        ? t("chatTabs.empty.setupGuide.backend.descriptionDone")
        : t("chatTabs.empty.setupGuide.backend.description"),
      status: backendDone ? "done" : "current",
      actionLabel: backendDone
        ? t("chatTabs.empty.setupGuide.actions.view")
        : t("chatTabs.empty.setupGuide.actions.configure"),
      onAction: goBackend,
    },
    {
      id: "provider",
      index: 2,
      title: t("chatTabs.empty.setupGuide.provider.title"),
      description: providerDone
        ? t("chatTabs.empty.setupGuide.provider.descriptionDone")
        : t("chatTabs.empty.setupGuide.provider.description"),
      status: providerDone ? "done" : backendDone ? "current" : "waiting",
      actionLabel: providerDone
        ? t("chatTabs.empty.setupGuide.actions.view")
        : backendDone
          ? t("chatTabs.empty.setupGuide.actions.configure")
          : t("chatTabs.empty.setupGuide.actions.waiting"),
      onAction: goProvider,
    },
    {
      id: "start",
      index: 3,
      title: t("chatTabs.empty.setupGuide.start.title"),
      description: t("chatTabs.empty.setupGuide.start.description"),
      status: canStart ? "current" : "waiting",
      actionLabel: canStart
        ? t("chatTabs.empty.setupGuide.actions.start")
        : t("chatTabs.empty.setupGuide.actions.waiting"),
      onAction: startFirstChat,
    },
  ];

  return (
    <>
      {/* 标题与说明跟着清单一起渲染：步数取自 steps 数组，加减一步文案自己跟着变，
          不会像写死的「两步」那样跟实际行数对不上。Fragment 不产生额外节点，
          DOM 结构与原先一致。 */}
      <div className="text-base font-semibold">
        {t("chatTabs.empty.setupGuide.title", { count: steps.length })}
      </div>
      <div className="max-w-md text-xs text-muted-foreground">
        {t("chatTabs.empty.setupGuide.description")}
      </div>
      <div className="w-full max-w-lg overflow-hidden rounded-lg border border-border bg-card text-left">
        {steps.map((step) => (
          <SetupChecklistRow key={step.id} {...step} />
        ))}
      </div>
    </>
  );
}

function SetupChecklistRow({
  id,
  index,
  title,
  description,
  status,
  actionLabel,
  onAction,
}: {
  id: string;
  index: number;
  title: string;
  description: string;
  status: SetupStepStatus;
  actionLabel: string;
  onAction: () => void;
}) {
  const done = status === "done";
  const waiting = status === "waiting";

  return (
    <div
      data-testid={`setup-step-${id}`}
      data-status={status}
      className={cn(
        "flex items-center gap-3 border-b border-border px-3.5 py-2.5 last:border-b-0",
        waiting && "bg-secondary/50",
      )}
    >
      <span
        aria-hidden="true"
        className={cn(
          "inline-flex size-5 shrink-0 items-center justify-center rounded-full border text-2xs font-semibold",
          done &&
            "border-status-running bg-status-running text-status-running-foreground",
          status === "current" &&
            "border-status-waiting-text text-status-waiting-text",
          waiting &&
            "border-dashed border-control-border text-muted-foreground",
        )}
      >
        {done ? <Check className="size-3" /> : index}
      </span>
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">{title}</div>
        <div className="text-xs text-muted-foreground">{description}</div>
      </div>
      <Button
        type="button"
        size="sm"
        variant={status === "current" ? "default" : "outline"}
        disabled={waiting}
        onClick={onAction}
      >
        {actionLabel}
      </Button>
    </div>
  );
}

const HostedPanel = React.memo(function HostedPanel({
  tab,
  active,
}: {
  tab: ChatTab;
  active: boolean;
}) {
  const sid = tab.meta.kind === "session" ? tab.meta.sessionId : 0;
  const isNewTab = tab.meta.kind === "new";
  const newAgentId = tab.meta.kind === "new" ? tab.meta.agentId : 0;
  const newProjectId = tab.meta.kind === "new" ? tab.meta.projectId : 0;
  const agents = useChatAgentsStore((s) => s.agents);
  const agentsLoading = useChatAgentsStore((s) => s.loading);
  const agentsError = useChatAgentsStore((s) => s.error);
  const reloadAgents = useChatAgentsStore((s) => s.reload);
  const agent = isNewTab
    ? (agents.find((a) => a.id === newAgentId) ?? null)
    : null;
  const resolveNewTab = useChatTabsStore((s) => s.resolveNewTab);
  const closeTab = useChatTabsStore((s) => s.closeTab);
  const openPeerTab = useChatTabsStore((s) => s.openPeerTab);
  const reloadMissingAgentRef = React.useRef<number | null>(null);
  const newSessionContext = React.useMemo(
    () => (isNewTab ? { projectId: newProjectId } : undefined),
    [isNewTab, newProjectId],
  );
  const handleSessionCreated = React.useCallback(
    (newSid: number) => resolveNewTab(tab.id, newSid),
    [resolveNewTab, tab.id],
  );
  // R18 桌面派发：新建会话派到另一台桌面端成功后，关掉新建 Tab 并打开 Peer Tab。
  const handlePeerSessionCreated = React.useCallback(
    (peer: {
      fingerprint: string;
      conversationId: string;
      title: string;
      deviceName: string;
    }) => {
      closeTab(tab.id);
      openPeerTab({
        fingerprint: peer.fingerprint,
        conversationId: peer.conversationId,
        title: peer.title,
        deviceName: peer.deviceName,
      });
    },
    [closeTab, openPeerTab, tab.id],
  );
  const handleSessionDeleted = React.useCallback(
    () => closeTab(tab.id),
    [closeTab, tab.id],
  );
  const handleSidebarShouldReload = React.useCallback(() => {
    // 统一信号: 让 /chat (chat-agents-store) 与 /projects
    // (session-index-store 已加载的 scope) 两条来源都同步刷新。新建会话 /
    // 删除会话 / 改标题 / turn 结束等 RPC 完成都走这里, 不必等下次
    // mount。两个 store 各自 inflight dedup, 调用安全。
    reloadSidebarSources();
  }, []);

  // 每次该 Tab 从隐藏切到 active(包括 tab-strip 单击、overflow menu、⌘1..⌘9、
  // cmd+click 新开后激活、closeTab 后自动激活相邻 tab),把焦点交回 TipTap 输入框。
  // ProseMirror contenteditable 支持原生 .focus(),光标停在上次位置即可,不需要
  // 强行跳到末尾。
  const wrapperRef = React.useRef<HTMLDivElement>(null);
  const prevActiveRef = React.useRef<boolean | null>(null);
  const prevNewAgentReadyRef = React.useRef(false);
  const newAgentReady = isNewTab && !!agent;
  React.useEffect(() => {
    const prev = prevActiveRef.current;
    const prevNewAgentReady = prevNewAgentReadyRef.current;
    prevActiveRef.current = active;
    prevNewAgentReadyRef.current = newAgentReady;
    if (
      !active ||
      (prev === true && (!isNewTab || prevNewAgentReady || !newAgentReady))
    ) {
      return;
    }
    const editor = wrapperRef.current?.querySelector<HTMLElement>(
      "[contenteditable='true']",
    );
    if (!editor) return;
    // 用 microtask 等 display:none → flex 切换完, Radix 菜单 / popover 关闭时
    // 的焦点夺回也已让出,再 focus 才能稳稳落到编辑器上。
    const id = window.setTimeout(() => editor.focus(), 0);
    return () => window.clearTimeout(id);
  }, [active, isNewTab, newAgentReady]);

  React.useEffect(() => {
    if (!isNewTab || agent) return;
    if (reloadMissingAgentRef.current === newAgentId) return;
    reloadMissingAgentRef.current = newAgentId;
    void reloadAgents();
  }, [agent, isNewTab, newAgentId, reloadAgents]);

  return (
    <div
      ref={wrapperRef}
      data-tab-id={tab.id}
      data-active={active}
      aria-hidden={!active}
      className={panelFrameClassName(active)}
    >
      {isNewTab && !agent ? (
        <MissingNewSessionAgent
          agentId={newAgentId}
          loading={agentsLoading}
          error={agentsError}
        />
      ) : (
        <ChatPanel
          active={active}
          scrollStateKey={tab.id}
          sessionId={sid}
          newSessionAgent={isNewTab ? agent : null}
          newSessionContext={newSessionContext}
          onSessionCreated={handleSessionCreated}
          onPeerSessionCreated={handlePeerSessionCreated}
          onSessionDeleted={handleSessionDeleted}
          onSidebarShouldReload={handleSidebarShouldReload}
        />
      )}
    </div>
  );
});

const HostedTerminalPanel = React.memo(function HostedTerminalPanel({
  tab,
  active,
}: {
  tab: ChatTab;
  active: boolean;
}) {
  const closeTab = useChatTabsStore((s) => s.closeTab);
  const meta = tab.meta as Extract<TabKind, { kind: "terminal" }>;
  const handleClose = React.useCallback(
    () => closeTab(tab.id),
    [closeTab, tab.id],
  );

  return (
    <div
      data-tab-id={tab.id}
      data-active={active}
      aria-hidden={!active}
      className={panelFrameClassName(active)}
    >
      <TerminalPanel
        terminalID={meta.terminalId}
        projectId={meta.projectId}
        deviceId={meta.deviceId}
        active={active}
        attach={meta.attach}
        onClose={handleClose}
      />
    </div>
  );
});

// HostedPeerPanel 承载一枚远端桌面会话 Peer Tab（R19）：attach/pull/live 由
// peer-session-store 管理，关闭 Tab 只 detach 本端接入、不删除对端会话。
const HostedPeerPanel = React.memo(function HostedPeerPanel({
  tab,
  active,
}: {
  tab: ChatTab;
  active: boolean;
}) {
  const closeTab = useChatTabsStore((s) => s.closeTab);
  const meta = tab.meta as Extract<TabKind, { kind: "peer" }>;
  const handleClose = React.useCallback(
    () => closeTab(tab.id),
    [closeTab, tab.id],
  );

  return (
    <div
      data-tab-id={tab.id}
      data-active={active}
      aria-hidden={!active}
      className={panelFrameClassName(active)}
    >
      <PeerPanel
        fingerprint={meta.fingerprint}
        conversationId={meta.conversationId}
        title={tab.title ?? ""}
        deviceName={meta.deviceName}
        active={active}
        onClose={handleClose}
      />
    </div>
  );
});

function panelFrameClassName(active: boolean): string {
  const base = "h-full min-h-0 flex-1 flex-col";
  if (active) return `${base} relative z-10 flex`;
  return `${base} invisible pointer-events-none absolute inset-0 z-0 flex overflow-hidden`;
}

function MissingNewSessionAgent({
  agentId,
  loading,
  error,
}: {
  agentId: number;
  loading: boolean;
  error: string | null;
}) {
  const { t } = useTranslation();
  const title = loading
    ? t("chatTabs.missingAgent.loading")
    : error
      ? t("chatTabs.missingAgent.loadFailed")
      : t("chatTabs.missingAgent.notFound");
  const detail = error
    ? error
    : loading
      ? `Agent #${agentId}`
      : t("chatTabs.missingAgent.detail", { id: agentId });

  return (
    <main className="flex min-h-0 min-w-0 flex-1 flex-col items-center justify-center gap-2 bg-background px-8 text-center">
      <div className="text-sm font-semibold">{title}</div>
      <div className="max-w-md text-xs text-muted-foreground">{detail}</div>
    </main>
  );
}
