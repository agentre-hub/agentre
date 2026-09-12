import { useUiTranslation } from "../i18n";
import {
  AGENTRED_DEPLOY_DOC_URL,
  AGENTRED_RELEASES_URL,
  agentredInstallCommand,
  agentredVersionCommand,
  type AgentredInstallMethod,
  type AgentredTargetOS,
} from "./agentred-commands";
import { ChoiceGroup } from "./choice-group";
import { CommandCard } from "./command-card";

export type AgentredInstallSectionProps = {
  method: AgentredInstallMethod;
  onMethodChange: (next: AgentredInstallMethod) => void;
  os: AgentredTargetOS;
  onOsChange: (next: AgentredTargetOS) => void;
  installTestId?: string;
  installCopyTestId?: string;
};

/**
 * 「把 agentred 装上」这一段，两端一字不差。
 *
 * 只给要执行的命令：一条装、一条自证。`install.sh` 在 `/usr/local/bin` 不可写时会
 * 自己印出改装到了哪、要 export 什么 PATH；`install.ps1` 自己会写用户 PATH 并让人
 * 开新终端。引导重复一遍只会把动作埋进说明里。
 */
export function AgentredInstallSection({
  method,
  onMethodChange,
  os,
  onOsChange,
  installTestId,
  installCopyTestId,
}: AgentredInstallSectionProps) {
  const { t } = useUiTranslation();
  const docker = method === "docker";

  return (
    <div className="flex flex-col gap-4">
      <ChoiceGroup<AgentredInstallMethod>
        label={t("onboarding.install.method")}
        value={method}
        choices={[
          { value: "native", label: t("onboarding.install.native") },
          { value: "docker", label: t("onboarding.install.docker") },
        ]}
        onChange={onMethodChange}
      />

      {/* 容器方式下宿主系统不改变任何命令，留着那排按钮只会让人以为它有用。 */}
      {docker ? null : (
        <ChoiceGroup<AgentredTargetOS>
          label={t("onboarding.install.os")}
          hint={t("onboarding.install.osHint")}
          value={os}
          choices={[
            { value: "linux", label: t("onboarding.install.linux") },
            { value: "macos", label: t("onboarding.install.macos") },
            { value: "windows", label: t("onboarding.install.windows") },
          ]}
          onChange={onOsChange}
        />
      )}

      <div className="flex flex-col gap-2">
        <CommandCard
          label={
            docker
              ? t("onboarding.install.pull")
              : os === "windows"
                ? t("onboarding.install.powershell")
                : t("onboarding.install.terminal")
          }
          command={agentredInstallCommand(method, os)}
          testId={installTestId}
          copyTestId={installCopyTestId}
        />
        <CommandCard
          label={
            docker
              ? t("onboarding.install.verifyImage")
              : t("onboarding.install.verify")
          }
          command={agentredVersionCommand(method)}
        />
      </div>
    </div>
  );
}

/**
 * 页脚那条外链。挂载、端口、`network_mode` 这些随部署形态变化的内容不进界面，
 * 由它把人送到 `deploy/README.md`；原生方式下换成发布页。
 */
export function AgentredInstallDocsLink({
  method,
  testId,
}: {
  method: AgentredInstallMethod;
  testId?: string;
}) {
  const { t } = useUiTranslation();
  const docker = method === "docker";

  return (
    <a
      href={docker ? AGENTRED_DEPLOY_DOC_URL : AGENTRED_RELEASES_URL}
      target="_blank"
      rel="noreferrer"
      data-testid={testId}
      className="text-xs font-medium text-primary-text hover:underline"
    >
      {docker
        ? t("onboarding.install.deployDoc")
        : t("onboarding.install.manual")}
    </a>
  );
}
