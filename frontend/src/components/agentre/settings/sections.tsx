// 各分区本体。多数是薄壳:取数据 + 交给被设置的那个组件,自己不画 UI。
//
// 它们都只被 SettingsPage 按当前页 id 选中一个渲染,所以按「一页一个函数」放在一起,
// 而不是一页一个文件 —— 每个都不足 40 行。

import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { ArrowRight } from "lucide-react";

import { Button } from "@agentre-hub/agentre-ui";

import type { AgentSlim } from "@/hooks/use-chat-agents";
import { openAgentSetup } from "@/lib/agent-setup-link";
import { closeSettings } from "@/stores/settings-dialog-store";
import { AgentBackendsPanel } from "../agent-backends";
import { AgentAvatar } from "../primitives";
import type { AgentColor } from "../types";
import { CtlSkillPanel } from "../ctl-skill";
import { LlmProvidersPanel } from "../llm-providers";
import { SettingsProxyPanel } from "../settings-proxy";
import { UnderConstructionPage } from "../under-construction-page";

import { SettingsPageHeader } from "./header";
import { underConstructionSettingsPages, type SettingsPageId } from "./pages";

export function AgentBackendSettings({
  onOpenLlmProviders,
  onOpenProxySettings,
}: {
  onOpenLlmProviders: () => void;
  onOpenProxySettings: () => void;
}) {
  const { t } = useTranslation();

  // 与 LLM 供应商页同一条规则：页级操作（自动识别 / 新建后端）属于 H1 行，卡片里不再
  // 重复一层页头。按钮开的弹窗和扫描进行态归面板自己管，所以页头交给面板渲染，
  // 面板只把按钮塞进 actions 槽，状态不用上提。
  return (
    <AgentBackendsPanel
      onOpenLlmProviders={onOpenLlmProviders}
      onOpenProxySettings={onOpenProxySettings}
      renderHeader={(actions) => (
        <SettingsPageHeader
          title={t("settings.agentBackend.title")}
          description={t("settings.agentBackend.description")}
          actions={actions}
        />
      )}
    />
  );
}

export function LocalProxySettings() {
  const { t } = useTranslation();

  return (
    <>
      <SettingsPageHeader
        title={t("settings.localProxy.title")}
        description={t("settings.localProxy.description")}
      />
      <SettingsProxyPanel />
    </>
  );
}

export function CtlSkillSettings() {
  const { t } = useTranslation();

  return (
    <>
      <SettingsPageHeader
        title={t("settings.ctlSkill.title")}
        description={t("settings.ctlSkill.description")}
      />
      <CtlSkillPanel />
    </>
  );
}

export function LlmProviderSettings({
  onOpenAgentBackends,
  providerGapAgents,
}: {
  onOpenAgentBackends: () => void;
  /** 缺已激活供应商的 Agent（按 isProviderGap 筛好），空 = 没有缺口。 */
  providerGapAgents: readonly AgentSlim[];
}) {
  const { t } = useTranslation();

  // 「新增供应商」是页级操作，位置属于 H1 行，但它开的创建弹窗归面板自己管：
  // 这里把页头(以及必须夹在页头与工作区之间的「待补齐」清单)交给面板渲染，面板只把
  // 按钮塞进 actions 槽，弹窗状态不用上提。
  return (
    <LlmProvidersPanel
      onOpenAgentBackends={onOpenAgentBackends}
      renderHeader={(actions) => (
        <>
          <SettingsPageHeader
            title={t("settings.llmProvider.title")}
            description={t("settings.llmProvider.description")}
            actions={actions}
          />
          {providerGapAgents.length > 0 ? (
            <ProviderGapList agents={providerGapAgents} />
          ) : null}
        </>
      )}
    />
  );
}

// 这一页管的缺口：后端绑了未激活 / 缺失的供应商。设置导航那颗点经 isProviderGap
// 用的是这同一张表。
const PROVIDER_GAP_COPY = new Map<string, string>([
  [
    "provider-inactive",
    "settings.llmProvider.gapList.reasons.providerInactive",
  ],
  [
    "backend-requires-provider",
    "settings.llmProvider.gapList.reasons.backendRequiresProvider",
  ],
]);

/** LLM 供应商页管的缺口（blockReason 为 provider-inactive / backend-requires-provider）。 */
export function isProviderGap(blockReason: string): boolean {
  return PROVIDER_GAP_COPY.has(blockReason);
}

/**
 * 「待补齐」：谁缺、缺哪一环、去哪修。每行直达组织架构里的那个 Agent —— 修的地方
 * 是 Agent 详情（给它选后端 / 供应商），不是这一页。数据与导航点同源（ListChatAgents）。
 */
function ProviderGapList({ agents }: { agents: readonly AgentSlim[] }) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const gaps = agents.flatMap((agent) => {
    const reasonKey = PROVIDER_GAP_COPY.get(agent.blockReason);
    return reasonKey ? [{ agent, reasonKey }] : [];
  });
  const title = t("settings.llmProvider.gapList.title");

  return (
    <section
      className="flex flex-col gap-1 rounded-lg bg-status-waiting-bg p-3"
      data-slot="llm-provider-gap-list"
    >
      <h2 className="px-1 text-xs font-semibold text-foreground">
        {t("settings.llmProvider.gapList.heading", { count: gaps.length })}
      </h2>
      <ul aria-label={title} className="flex flex-col">
        {gaps.map(({ agent, reasonKey }) => (
          <li
            key={agent.id}
            className="flex min-w-0 items-center gap-2 rounded-md px-1 py-1 text-xs"
          >
            <AgentAvatar
              name={agent.name}
              color={agent.avatarColor as AgentColor}
              avatarIcon={agent.avatarIcon}
              avatarDataUrl={agent.avatarDataUrl}
              size="xs"
            />
            <span className="min-w-0 truncate font-medium">{agent.name}</span>
            <span className="min-w-0 flex-1 truncate text-muted-foreground">
              {t(reasonKey)}
            </span>
            <Button
              type="button"
              variant="link"
              className="h-auto shrink-0 gap-1 px-0 text-xs"
              aria-label={t("settings.llmProvider.gapList.configureAria", {
                name: agent.name,
              })}
              onClick={() => {
                // 先关设置再跳转：对话框不能带着自己落到组织页上。
                closeSettings();
                openAgentSetup(navigate, agent.id);
              }}
            >
              {t("settings.llmProvider.gapList.configure")}
              <ArrowRight className="size-3" aria-hidden="true" />
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}

export function SettingsUnderConstruction({ page }: { page: SettingsPageId }) {
  const { t } = useTranslation();

  if (
    page === "appearance" ||
    page === "agent-backend" ||
    page === "remote-devices" ||
    page === "files" ||
    page === "keyboard-shortcuts" ||
    page === "llm-providers" ||
    page === "local-proxy" ||
    page === "version-logs" ||
    page === "data-backup" ||
    page === "notifications" ||
    page === "skills-tools" ||
    page === "sync"
  ) {
    return null;
  }

  const pageConfig = underConstructionSettingsPages[page];

  return (
    <UnderConstructionPage
      className="px-0 py-0"
      description={t(pageConfig.descriptionKey)}
      icon={pageConfig.icon}
      title={t(pageConfig.titleKey)}
    />
  );
}
