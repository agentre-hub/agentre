import { useUiTranslation } from "../i18n";
import {
  agentredCommands,
  type AgentredInstallMethod,
  type AgentredRunMode,
} from "./agentred-commands";
import { ChoiceGroup } from "./choice-group";
import { CommandCard } from "./command-card";

export type AgentredServiceSectionProps = {
  method: AgentredInstallMethod;
  runMode?: AgentredRunMode;
  /**
   * 省略即不渲染运行方式选择器，只给后台那条路。控制台就是这种：它的设备要长期
   * 在线，一个会随终端关闭而消失的选项对它没有意义。**没有端口就没有这个能力**，
   * 而不是在共享组件里判断自己跑在哪个宿主。
   */
  onRunModeChange?: (next: AgentredRunMode) => void;
  serviceTestId?: string;
  serviceCopyTestId?: string;
};

/**
 * 「让它常驻」这一段。原生走 `agentred service`，容器走 `docker compose`。
 *
 * 同样只给命令：`service install` 自己会在 Linux 上印出
 * `Run: loginctl enable-linger <用户>`，`compose ps` 自己会说容器在不在跑。
 */
export function AgentredServiceSection({
  method,
  runMode = "service",
  onRunModeChange,
  serviceTestId,
  serviceCopyTestId,
}: AgentredServiceSectionProps) {
  const { t } = useUiTranslation();
  const docker = method === "docker";
  // 容器方式没有「前台临时」这回事——compose 起的就是后台。
  const foreground = !docker && runMode === "foreground";

  return (
    <div className="flex flex-col gap-4">
      {!docker && onRunModeChange ? (
        <ChoiceGroup<AgentredRunMode>
          label={t("onboarding.service.mode")}
          value={runMode}
          choices={[
            { value: "service", label: t("onboarding.service.background") },
            { value: "foreground", label: t("onboarding.service.foreground") },
          ]}
          onChange={onRunModeChange}
        />
      ) : null}

      {foreground ? (
        <CommandCard
          label={t("onboarding.install.terminal")}
          command={agentredCommands.foregroundRun}
        />
      ) : (
        <div className="flex flex-col gap-3">
          <CommandCard
            label={
              docker
                ? t("onboarding.service.composeUp")
                : t("onboarding.service.install")
            }
            command={
              docker
                ? agentredCommands.composeUp
                : agentredCommands.serviceInstall
            }
            testId={serviceTestId}
            copyTestId={serviceCopyTestId}
          />
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            <CommandCard
              label={t("onboarding.service.status")}
              command={
                docker
                  ? agentredCommands.composePs
                  : agentredCommands.serviceStatus
              }
            />
            <CommandCard
              label={
                docker
                  ? t("onboarding.service.composeLogs")
                  : t("onboarding.service.restart")
              }
              command={
                docker
                  ? agentredCommands.composeLogs
                  : agentredCommands.serviceRestart
              }
            />
          </div>
        </div>
      )}
    </div>
  );
}
