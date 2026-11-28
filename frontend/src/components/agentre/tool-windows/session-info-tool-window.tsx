import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  AgentAvatar,
  Button,
  PERMISSION_MODE_META_UI,
  Popover,
  PopoverContent,
  PopoverTrigger,
  fallbackPermissionModeMetaUI,
  formatTokens,
} from "@agentre-hub/agentre-ui";
import { ListAgentBackends } from "@/../wailsjs/go/app/App";
import { useSessionRoot } from "@/stores/session-root-store";

import type { chat_svc } from "../../../../wailsjs/go/models";

import type { SessionUsageTotals } from "./session-usage";

/**
 * 会话信息工具窗吃的数据。其中模型 / 思考力度 / 权限 / 上下文用量取自输入框旁
 * 那几颗控件**同一份**状态（由 ChatPanel 传入），所以两处标签与数值逐字一致；
 * 某项传 undefined 即「不可得」，对应的行不画。
 */
export type SessionInfoData = {
  session: chat_svc.ChatSessionDetail | null;
  /** 输入框旁权限药丸当前的档位；输入框不显示该控件时为 undefined。 */
  permissionMode?: string;
  /** 输入框模型药丸脸上的模型名。 */
  modelLabel?: string;
  /** 有效思考力度（会话值优先，否则后端配置）；空串 / undefined = 没有可说的档位。 */
  reasoningEffort?: string;
  /** 输入框底栏上下文计量器用的 {已用, 窗口}。 */
  contextUsage?: { used: number; max: number };
  /** 轮数 = 用户消息数。 */
  turns: number;
  /** 累计用量；没有任何记录时为 null。 */
  usage: SessionUsageTotals | null;
  /** 「复制启动命令」：与标题栏菜单同一个处理函数。 */
  onCopyLaunchCommand?: () => void;
};

type Props = {
  sessionId: number;
  info: SessionInfoData;
};

/** 与标题栏菜单一致：只有这三类 CLI 后端有「启动命令」。 */
const LAUNCH_COMMAND_BACKENDS = new Set(["claudecode", "codex", "piagent"]);

/** 用户给这个后端起的名字。查不到（没有这个后端 / 请求失败）返回 null，该行不显示。 */
function useBackendName(agentBackendId: number): string | null {
  const [name, setName] = React.useState<string | null>(null);
  React.useEffect(() => {
    let cancelled = false;
    setName(null);
    if (agentBackendId <= 0) return;
    ListAgentBackends()
      .then((res) => {
        if (cancelled) return;
        const found = res?.items?.find((b) => b.id === agentBackendId);
        setName(found?.name ? found.name : null);
      })
      .catch(() => {
        if (!cancelled) setName(null);
      });
    return () => {
      cancelled = true;
    };
  }, [agentBackendId]);
  return name;
}

function Row({
  rowKey,
  label,
  children,
}: {
  rowKey: string;
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div
      data-testid={`session-info-row-${rowKey}`}
      className="flex flex-col gap-0.5 px-3 py-1.5"
    >
      <dt className="text-3xs font-semibold tracking-wide text-muted-foreground">
        {label}
      </dt>
      {/* 值只读、可选中复制（不放复制按钮）。 */}
      <dd
        data-testid="session-info-value"
        data-selectable-text="true"
        className="m-0 min-w-0 text-xs break-words text-foreground"
      >
        {children}
      </dd>
    </div>
  );
}

function ContextBar({ used, max }: { used: number; max: number }) {
  const ratio = max > 0 ? Math.min(1, Math.max(0, used) / max) : 0;
  return (
    <div className="flex flex-col gap-1">
      <div
        aria-hidden="true"
        className="h-1 w-full overflow-hidden rounded-full bg-muted"
      >
        <div
          className="h-full rounded-full bg-primary"
          style={{ width: `${Math.round(ratio * 100)}%` }}
        />
      </div>
      <span className="font-mono tabular-nums">
        {formatTokens(Math.max(0, used))} / {formatTokens(max)}
      </span>
    </div>
  );
}

/** sections 的每一层是 [层标题, 条目…]（见 ChatSessionDetail.carriedMemory）。 */
function CarriedMemory({ sections }: { sections: string[][] }) {
  const { t } = useTranslation();
  const count = sections.reduce((n, s) => n + s.length - 1, 0);
  if (count === 0) return <>{t("sessionInfoToolWindow.memory.none")}</>;
  return (
    <span className="flex items-center gap-2">
      <span>{t("sessionInfoToolWindow.memory.count", { count })}</span>
      <Popover>
        <PopoverTrigger asChild>
          <Button type="button" variant="ghost" size="xs">
            {t("sessionInfoToolWindow.memory.view")}
          </Button>
        </PopoverTrigger>
        <PopoverContent
          align="end"
          className="max-h-80 w-80 overflow-auto p-3"
          data-testid="session-info-memory-view"
          data-selectable-text="true"
        >
          <p className="pb-2 text-xs font-semibold">
            {t("sessionInfoToolWindow.memory.title")}
          </p>
          {sections.map(([heading, ...entries]) => (
            <section key={heading} className="pb-2">
              <h5 className="text-3xs font-semibold text-muted-foreground">
                {heading}
              </h5>
              <ul className="m-0 list-none p-0 text-xs">
                {entries.map((entry) => (
                  <li key={entry} className="py-0.5">
                    {entry}
                  </li>
                ))}
              </ul>
            </section>
          ))}
        </PopoverContent>
      </Popover>
    </span>
  );
}

/**
 * 「会话信息」工具窗：只读键值列表（spec「会话信息工具窗」）。每个值都是可选中的
 * 文本；某项数据不可得时整行不显示，从不编一个值。
 */
export function SessionInfoToolWindow({ sessionId, info }: Props) {
  const { t } = useTranslation();
  const { session } = info;
  const backendName = useBackendName(session?.agentBackendId ?? 0);
  const { roots, currentRoot } = useSessionRoot(sessionId);

  if (!session) return null;

  const permissionLabel =
    info.permissionMode === undefined
      ? null
      : (
          PERMISSION_MODE_META_UI[info.permissionMode] ??
          fallbackPermissionModeMetaUI(info.permissionMode)
        ).label;

  const modelText = info.modelLabel
    ? info.reasoningEffort
      ? `${info.modelLabel} · ${info.reasoningEffort}`
      : info.modelLabel
    : null;

  const otherWorktrees = currentRoot
    ? roots.filter((r) => r.isWorktree && r.path !== currentRoot.path).length
    : 0;

  const carried = session.carriedMemory ?? [];
  const canCopyLaunch =
    LAUNCH_COMMAND_BACKENDS.has(session.backendType) &&
    info.onCopyLaunchCommand !== undefined;

  return (
    <div className="min-h-0 flex-1 overflow-auto py-1">
      <dl className="m-0 flex flex-col">
        <Row rowKey="agent" label={t("sessionInfoToolWindow.rows.agent")}>
          <span className="flex items-center gap-1.5">
            <AgentAvatar
              name={session.agentName}
              color={session.agentColor}
              iconKey={session.agentIcon}
              avatarDataUrl={session.agentAvatarDataUrl}
              size="sm"
            />
            <span>{session.agentName}</span>
          </span>
        </Row>
        {backendName ? (
          <Row rowKey="backend" label={t("sessionInfoToolWindow.rows.backend")}>
            {backendName} ·{" "}
            {session.deviceID
              ? session.deviceName || session.deviceID
              : t("deviceTag.local")}
          </Row>
        ) : null}
        {modelText ? (
          <Row rowKey="model" label={t("sessionInfoToolWindow.rows.model")}>
            {modelText}
          </Row>
        ) : null}
        {permissionLabel ? (
          <Row
            rowKey="permission"
            label={t("sessionInfoToolWindow.rows.permission")}
          >
            {permissionLabel}
          </Row>
        ) : null}
        {info.contextUsage && info.contextUsage.max > 0 ? (
          <Row rowKey="context" label={t("sessionInfoToolWindow.rows.context")}>
            <ContextBar
              used={info.contextUsage.used}
              max={info.contextUsage.max}
            />
          </Row>
        ) : null}
        {info.usage ? (
          <Row rowKey="usage" label={t("sessionInfoToolWindow.rows.usage")}>
            {t("sessionInfoToolWindow.usage", {
              input: formatTokens(info.usage.input),
              output: formatTokens(info.usage.output),
              cache: formatTokens(info.usage.cacheHits),
            })}
          </Row>
        ) : null}
        <Row rowKey="memory" label={t("sessionInfoToolWindow.rows.memory")}>
          <CarriedMemory sections={carried} />
        </Row>
        {currentRoot ? (
          <Row
            rowKey="location"
            label={t("sessionInfoToolWindow.rows.location")}
          >
            {currentRoot.name} ·{" "}
            {currentRoot.isWorktree
              ? t("chatContext.workRoots.worktree")
              : t("chatContext.workRoots.mainRepo")}
            {otherWorktrees > 0
              ? ` · ${t("sessionInfoToolWindow.otherWorktrees", { count: otherWorktrees })}`
              : ""}
          </Row>
        ) : null}
        {session.createtime > 0 ? (
          <Row rowKey="created" label={t("sessionInfoToolWindow.rows.created")}>
            {new Date(session.createtime).toLocaleString()} ·{" "}
            {t("sessionInfoToolWindow.turns", { count: info.turns })}
          </Row>
        ) : null}
        {session.providerSessionId ? (
          <Row
            rowKey="cliSession"
            label={t("sessionInfoToolWindow.rows.cliSession")}
          >
            <span className="font-mono">{session.providerSessionId}</span>
          </Row>
        ) : null}
      </dl>
      {canCopyLaunch ? (
        <div className="px-3 pt-2 pb-1">
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={info.onCopyLaunchCommand}
          >
            {t("chatPanel.launchCommand.copy")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
