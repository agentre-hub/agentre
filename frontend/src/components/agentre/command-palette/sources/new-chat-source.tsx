import * as React from "react";
import type { TFunction } from "i18next";
import { useTranslation } from "react-i18next";

import { groupAgentsForPicking } from "@agentre-ai/agentre-ui";

import { Badge } from "@/components/ui/badge";
import { useChatAgents, type ChatAgentItem } from "@/hooks/use-chat-agents";
import i18n from "@/i18n";
import { cn } from "@/lib/utils";
import {
  readLastAgentId,
  writeLastAgentId,
} from "@/stores/last-agent-persistence";

import { blockReasonToCta } from "../../not-chattable";
import { AgentAvatar } from "../../primitives";
import type { AgentColor } from "../../types";
import { scoreItem } from "../score";
import type { CommandSource, OnSelectCtx } from "../types";

// 命令面板的 "New chat with <agent>" 命令源 —— 自由会话版。
// 仅在非 /projects 路由激活（项目路由用 newProjectChatSource，命令名 "New project chat with"）。
// 不读 useNewChatContextStore：即便残留 projectContext 也忽略，
// 该 source 的语义就是"不带项目作用域的新会话"。
export type NewChatItem = {
  key: string;
  agentId: number;
  agent: ChatAgentItem;
  // 自由会话版总把 agent 视为"成员"（无项目分组概念）；保留字段是为了 renderItem 同构。
  isMember: true;
  // 不可对话 agent 带「需要先配置」次级分组标签（复用 subHeading 机制）。
  subHeading?: string;
};

// 排序：可对话组 lastAgent → pinned → others（历史行为不变）；
// 不可对话组 pinned → others（lastAgentId 只在可对话组内冒泡）。
// 两组同属 newChatSource，不可对话组用 subHeading 单列「需要先配置」。
//
// 分组与排序本身走共享包的 `groupAgentsForPicking`：agentre-server 的新对话列表
// 是同一条规则（判据是 has_available_target 而不是 chattable，结论同名），两端
// 此前各写了一份。呈现仍留在这里 —— 那边把「最近用过」单列一组带标题，这里冒泡
// 进同一组，这是两个产品面的决定，不是同一条规则的两种写法。
export function flattenAgents(
  agents: ChatAgentItem[],
  lastAgentId: number | null = null,
): NewChatItem[] {
  const { recent, available, unavailable } = groupAgentsForPicking({
    agents,
    key: (a) => String(a.id),
    available: (a) => a.chattable,
    pinned: (a) => a.pinned,
    // 本面只认「上次选过的那一个」，所以 recentKeys 至多一项 —— 它在这里的作用
    // 就是冒泡到最前，与 server 那边「最近用过」是同一格。
    recentKeys: lastAgentId != null ? [String(lastAgentId)] : [],
  });
  const needSetupHeading = i18n.t("commandPalette.newChat.needSetup");

  return [
    ...[...recent, ...available].map((agent) => ({
      key: `new-chat-agent-${agent.id}`,
      agentId: agent.id,
      agent,
      isMember: true as const,
    })),
    ...unavailable.map((agent) => ({
      key: `new-chat-agent-${agent.id}`,
      agentId: agent.id,
      agent,
      isMember: true as const,
      subHeading: needSetupHeading,
    })),
  ];
}

function useItems(): { items: NewChatItem[]; loading: boolean } {
  const { agents, loading } = useChatAgents();
  // useState 懒初始化：同一次面板打开期间锁定值，避免 useChatAgents 更新
  // 触发"上次选过"位置漂移。下次面板打开时（组件 remount）重新读取。
  const [lastAgentId] = React.useState(() => readLastAgentId());
  const items = React.useMemo(
    () => flattenAgents(agents, lastAgentId),
    [agents, lastAgentId],
  );
  return { items, loading };
}

function actionTitle(item: NewChatItem): string {
  return i18n.t(
    item.agent.chattable
      ? "commandPalette.newChat.itemTitle"
      : "commandPalette.newChat.needSetupTitle",
    { agentName: item.agent.name },
  );
}

const MULTI_TOKEN_SCORE = 25;

function getScore(query: string, item: NewChatItem): number {
  const title = actionTitle(item);
  const direct = scoreItem({
    query,
    title,
    subtitle: item.agent.name,
  });
  if (direct > 0) return direct;

  // Token-based fallback：多词查询每个 token 都需在 title 里出现（case-insensitive）。
  const tokens = query.toLowerCase().split(/\s+/).filter(Boolean);
  if (tokens.length <= 1) return 0;
  const tl = title.toLowerCase();
  for (const tok of tokens) {
    if (!tl.includes(tok)) return 0;
  }
  return MULTI_TOKEN_SCORE;
}

function renderItem(item: NewChatItem): React.ReactNode {
  return <AgentRow item={item} />;
}

type AgentRowProps = { item: NewChatItem };

// 不可对话行：原因优先取 blockReason 映射的短文案（复用 task 2 的 copyKey），
// blockReason 缺失时兜底用 chattableHint（后端总在同一点位设置二者）。
function reasonText(agent: ChatAgentItem, t: TFunction): string {
  if (agent.blockReason) {
    const key = `${blockReasonToCta(agent.blockReason).copyKey}.short`;
    const resolved = t(key);
    if (resolved !== key) return resolved;
  }
  return agent.chattableHint || "";
}

function AgentRow({ item }: AgentRowProps) {
  const { t } = useTranslation();
  const a = item.agent;
  const needSetup = !a.chattable;
  const needSetupTitlePrefix = t("commandPalette.newChat.needSetupTitlePrefix");
  const needSetupTitleSuffix = t("commandPalette.newChat.needSetupTitleSuffix");
  return (
    <div
      data-testid={`agent-picker-item-${a.id}`}
      className="flex w-full items-center gap-3"
    >
      <AgentAvatar
        name={a.name}
        initials={a.name.charAt(0)}
        color={(a.avatarColor as AgentColor) || "agent-1"}
        avatarIcon={a.avatarIcon || undefined}
        avatarDataUrl={a.avatarDataUrl || undefined}
        size="md"
        className="size-7 rounded-md text-xs"
      />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div
          className="truncate text-sm text-foreground"
          title={t(
            needSetup
              ? "commandPalette.newChat.needSetupTitle"
              : "commandPalette.newChat.itemTitle",
            { agentName: a.name },
          )}
        >
          {needSetup ? (
            <>
              <span className="text-muted-foreground">
                {needSetupTitlePrefix}{" "}
              </span>
              <span className="font-medium">{a.name}</span>
              {needSetupTitleSuffix ? (
                <span> {needSetupTitleSuffix}</span>
              ) : null}
            </>
          ) : (
            <>
              <span className="text-muted-foreground">
                {t("commandPalette.newChat.itemPrefix")}{" "}
              </span>
              <span className="font-medium">{a.name}</span>
            </>
          )}
        </div>
        {needSetup ? (
          <div className="truncate text-2xs text-muted-foreground">
            {reasonText(a, t)}
          </div>
        ) : a.chattableHint ? (
          <div className="truncate text-2xs text-muted-foreground">
            {a.chattableHint}
          </div>
        ) : null}
      </div>
      {needSetup ? (
        <Badge
          variant="outline"
          className="border-status-waiting/40 bg-status-waiting-bg px-1.5 py-0 text-2xs text-foreground"
        >
          {t("commandPalette.newChat.needSetupBadge")}
        </Badge>
      ) : null}
      <kbd
        className={cn(
          "rounded-sm border border-border bg-card px-1.5 py-0.5 font-mono text-2xs font-medium text-muted-foreground",
          "opacity-0 group-data-[selected=true]/cmditem:opacity-100",
        )}
        aria-hidden="true"
      >
        ↵
      </kbd>
    </div>
  );
}

function onSelect(item: NewChatItem, ctx: OnSelectCtx): void {
  ctx.close();
  // 不可对话：不建会话，打开任务 2 的引导弹窗（面板关闭、弹窗接管）。
  if (!item.agent.chattable) {
    ctx.openNotChattableDialog(item.agent);
    return;
  }
  writeLastAgentId(item.agentId);
  ctx.openNewSession(item.agentId);
  try {
    ctx.navigate("/chat");
  } catch (err) {
    console.warn("[command-palette] navigate('/chat') failed", err);
  }
}

export const newChatSource: CommandSource<NewChatItem> = {
  id: "new-chat",
  heading: i18n.t("commandPalette.newChat.heading"),
  modes: ["command"],
  activeFor: (ctx) => !ctx.hasProjectContext,
  useItems,
  getScore,
  renderItem,
  onSelect,
};
