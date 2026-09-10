// 各分区本体。多数是薄壳:取数据 + 交给被设置的那个组件,自己不画 UI。
//
// 它们都只被 SettingsPage 按当前页 id 选中一个渲染,所以按「一页一个函数」放在一起,
// 而不是一页一个文件 —— 每个都不足 40 行。

import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";
import { AlertCircle } from "lucide-react";

import {
  Alert,
  AlertDescription,
  AlertTitle,
  Button,
} from "@agentre-hub/agentre-ui";

import { AgentBackendsPanel } from "../agent-backends";
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
  providerGap,
}: {
  onOpenAgentBackends: () => void;
  providerGap: boolean;
}) {
  const { t } = useTranslation();
  const navigate = useNavigate();

  // 「新增供应商」是页级操作，位置属于 H1 行，但它开的创建弹窗归面板自己管：
  // 这里把页头(以及必须夹在页头与工作区之间的黄条)交给面板渲染，面板只把按钮
  // 塞进 actions 槽，弹窗状态不用上提。
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
          {providerGap ? (
            <Alert className="border-status-waiting/40 bg-status-waiting/10 text-status-waiting">
              <AlertCircle className="size-4" aria-hidden="true" />
              <AlertTitle className="text-xs font-semibold">
                {t("settings.llmProvider.gapBanner.title")}
              </AlertTitle>
              <AlertDescription className="text-2xs leading-relaxed">
                {t("settings.llmProvider.gapBanner.description")}
                <Button
                  type="button"
                  variant="link"
                  className="h-auto px-0 text-2xs"
                  onClick={() => navigate("/org")}
                >
                  {t("settings.llmProvider.gapBanner.goToOrg")}
                </Button>
              </AlertDescription>
            </Alert>
          ) : null}
        </>
      )}
    />
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
