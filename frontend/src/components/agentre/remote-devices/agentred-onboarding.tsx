import { useState } from "react";
import { ArrowRight } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  AgentredInstallDocsLink,
  AgentredInstallSection,
  AgentredServiceSection,
  Button,
  CommandCard,
  GuideStepRail,
  Separator,
  agentredPairCommand,
  type AgentredInstallMethod,
  type AgentredRunMode,
  type AgentredTargetOS,
  type GuideStep,
} from "@agentre-hub/agentre-ui";

import { DevicePairingForm, type AddRequest } from "./device-pairing-form";

type OnboardingStep = 1 | 2 | 3;

type AgentredOnboardingProps = {
  /** 省略即不可收起 —— 宿主没有可回退的地方(设备列表为空)时就是这种情况。 */
  onDismiss?: () => void;
  onSubmit: (request: AddRequest) => Promise<void>;
};

/**
 * 局域网直连的三步接入引导:安装 → 常驻 → 配对。
 *
 * 顺序被 agentred 的状态时序钉死,不是排版偏好:`agentred pair` 走本地 IPC 向**正在
 * 运行的** daemon 取一次性码,所以配对必须排在服务起来之后。
 *
 * 安装与常驻两段归共享包 —— agentre-server 的引导渲染的是同一份,命令也只有那一份。
 * 配对表单、TLS 信任与 Wails 调用是桌面端独有的,留在这里。
 */
export function AgentredOnboarding({
  onDismiss,
  onSubmit,
}: AgentredOnboardingProps) {
  const { t } = useTranslation();
  const [step, setStep] = useState<OnboardingStep>(1);
  // 只记用户真的点过「下一步」的步骤 —— 跳过去的步骤没有资格自称完成。
  const [finishedSteps, setFinishedSteps] = useState<readonly OnboardingStep[]>(
    [],
  );
  const [method, setMethod] = useState<AgentredInstallMethod>("native");
  const [remoteOS, setRemoteOS] = useState<AgentredTargetOS>("linux");
  const [runMode, setRunMode] = useState<AgentredRunMode>("service");
  // 配对在途时锁住所有离开第 3 步的路。失败原因只存在于那张表单里,一旦提前卸载
  // 它,setError 就落在已卸载的组件上,配对失败会被无声吞掉。
  const [submitting, setSubmitting] = useState(false);

  const finishStep = (finished: OnboardingStep, next: OnboardingStep) => {
    setFinishedSteps((previous) =>
      previous.includes(finished) ? previous : [...previous, finished],
    );
    setStep(next);
  };

  const steps: readonly GuideStep[] = [
    {
      key: "install",
      title: t("remoteDevices.onboarding.steps.install.title"),
      hint: t("remoteDevices.onboarding.steps.install.subtitle"),
      doneLabel: t("remoteDevices.onboarding.steps.install.done"),
    },
    {
      key: "service",
      title: t("remoteDevices.onboarding.steps.service.title"),
      hint: t("remoteDevices.onboarding.steps.service.subtitle"),
      doneLabel: t("remoteDevices.onboarding.steps.service.done"),
    },
    {
      key: "pair",
      title: t("remoteDevices.onboarding.steps.pair.title"),
      hint: t("remoteDevices.onboarding.steps.pair.subtitle"),
    },
  ];

  return (
    <section className="overflow-hidden rounded-xl border border-border bg-card">
      <GuideStepRail
        steps={steps}
        current={step}
        done={finishedSteps}
        disabled={submitting}
        onDismiss={onDismiss}
        onSelect={(next) => setStep(next as OnboardingStep)}
      />

      <div className="flex flex-col gap-5 p-4 sm:p-6">
        <div className="flex flex-col gap-2">
          <p className="font-mono text-2xs font-semibold uppercase tracking-[0.1em] text-primary-text">
            {t("remoteDevices.onboarding.stepLabel", {
              current: step,
              total: 3,
            })}
          </p>
          <h2 className="text-lg font-semibold">
            {step === 1
              ? t("remoteDevices.onboarding.install.title")
              : step === 2
                ? t("remoteDevices.onboarding.service.title")
                : t("remoteDevices.onboarding.pair.title")}
          </h2>
          {step === 2 ? null : (
            <p className="text-sm leading-relaxed text-muted-foreground">
              {step === 1
                ? t("remoteDevices.onboarding.install.description")
                : t("remoteDevices.onboarding.pair.description")}
            </p>
          )}
        </div>

        {step === 1 ? (
          <div className="flex flex-col gap-5">
            <AgentredInstallSection
              method={method}
              onMethodChange={setMethod}
              os={remoteOS}
              onOsChange={setRemoteOS}
            />
            <Separator />
            <div className="flex flex-wrap items-center justify-between gap-3">
              <AgentredInstallDocsLink method={method} />
              <Button type="button" onClick={() => finishStep(1, 2)}>
                {t("remoteDevices.onboarding.install.next")}
                <ArrowRight data-icon="inline-end" aria-hidden="true" />
              </Button>
            </div>
          </div>
        ) : null}

        {step === 2 ? (
          <div className="flex flex-col gap-5">
            <AgentredServiceSection
              method={method}
              runMode={runMode}
              onRunModeChange={setRunMode}
            />
            <Separator />
            <div className="flex justify-end">
              <Button type="button" onClick={() => finishStep(2, 3)}>
                {t("remoteDevices.onboarding.service.next")}
                <ArrowRight data-icon="inline-end" aria-hidden="true" />
              </Button>
            </div>
          </div>
        ) : null}

        {step === 3 ? (
          <div className="flex flex-col gap-4">
            <CommandCard
              label={t("remoteDevices.onboarding.pair.commandLabel")}
              command={agentredPairCommand(method)}
            />
            <div className="rounded-lg border border-border bg-muted/30 p-4">
              <DevicePairingForm
                actionsClassName="justify-between"
                cancelLabel={t("remoteDevices.onboarding.pair.back")}
                onCancel={() => setStep(2)}
                onSubmit={onSubmit}
                onSubmittingChange={setSubmitting}
                submitLabel={t("remoteDevices.onboarding.pair.submit")}
              />
            </div>
          </div>
        ) : null}
      </div>
    </section>
  );
}
