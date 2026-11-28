import { useTranslation } from "react-i18next";
import {
  AgentBackendLogo,
  LlmModelLogo,
  ProviderPillResolution,
  ProviderPillTrigger,
} from "@agentre-hub/agentre-ui";
import { cn } from "@agentre-hub/agentre-ui";

import type { UseProviderPillReturn } from "./use-provider-pill";
import { ModelTargetPicker } from "@agentre-hub/agentre-ui";

export type ProviderPillProps = UseProviderPillReturn;

/**
 * ProviderPill: composer 动作行里的 LLM ModelTarget 选择器。
 *
 * 常驻触发按钮直接呈现四态与解析结果：跟随 Agent、跟随供应商默认、固定模型、失效。
 * 共享 ModelTargetPicker 继续承载搜索、最近使用、远端门控与键盘交互。
 */
export function ProviderPill({
  cliModels,
  providerKey,
  modelKey,
  setTarget,
  backendType,
  catalog,
  loading,
  catalogLoading,
  catalogError,
  error,
  switchError,
  unbound,
  effectiveKey,
  pillState,
  boundResolutionLabel,
  boundProviderType,
  boundProviderLabel,
  boundModelLabel,
  boundCliLogin,
  invalid,
  disabled,
  disabledReason,
  executionLocation = "",
  deviceLabel,
  remoteCatalog,
  supportsFixedModel = true,
  remoteMissing = false,
}: ProviderPillProps) {
  const { t } = useTranslation();

  const disabledTitle =
    disabled && disabledReason === "unsupportedBackend"
      ? t("providerPill.disabledUnsupportedBackend")
      : disabled && disabledReason === "noCompatibleProviders"
        ? t("providerPill.disabledNoCompatibleProviders")
        : null;

  const ariaValue =
    pillState.resolutionLabel ||
    effectiveKey ||
    (unbound ? t("providerPill.unselected") : t("providerPill.unselected"));

  // 触发器与「跟随 Agent 绑定」那一项的解析副行都住在共享包里：那两格是用户一眼
  // 看到的东西，两端不同源就等于同一条对话在桌面端与浏览器里说两句话。宿主这边只
  // 负责把视图模型递进去，以及同步弹窗、持久化与重载这些宿主自己的事。
  const specialSublabel = (
    <ProviderPillResolution
      boundProviderType={boundProviderType}
      boundProviderLabel={boundProviderLabel}
      boundModelLabel={boundModelLabel}
      boundCliLogin={boundCliLogin}
      fallbackLabel={boundResolutionLabel}
    />
  );

  return (
    <>
      <ModelTargetPicker
        scenario="chat"
        cliModels={cliModels}
        specialIcon={
          cliModels ? (
            boundModelLabel ? (
              <LlmModelLogo
                providerType=""
                model={boundModelLabel}
                className="size-3.5"
              />
            ) : (
              <AgentBackendLogo
                backendType={backendType}
                className="size-3.5"
              />
            )
          ) : undefined
        }
        backendType={backendType}
        executionLocation={executionLocation}
        deviceLabel={deviceLabel}
        selected={{ providerKey, modelKey }}
        onChange={setTarget}
        catalog={catalog}
        loading={loading || catalogLoading}
        error={catalogError || error !== null}
        errorText={error ?? undefined}
        switchError={switchError ?? undefined}
        disabled={disabled}
        invalid={invalid}
        specialSublabel={specialSublabel}
        remoteCatalog={remoteCatalog}
        supportsFixedModel={supportsFixedModel}
        remoteMissing={remoteMissing}
        triggerLabel={<ProviderPillTrigger state={pillState} />}
        title={disabledTitle ?? undefined}
        footer={t("providerPill.switchNote")}
        aria-label={t("providerPill.aria", { provider: ariaValue })}
        data-testid="provider-pill"
        className={cn(
          // mockup ?view=chat 的 .pill：中性描边 + card 底，全程一个样 —— 选没选、
          // 跟随还是固定，一律不靠常亮边框宣示，那份区分由图标与 ↻ 承担。
          // 悬停只加深背景，不动边框（边框式反馈是明确否掉的）；focus-visible ring
          // 由 Picker 基类提供，键盘定位不受影响。
          "h-[26px] w-auto cursor-pointer gap-1.5 rounded-md px-2.5 text-2xs font-medium",
          invalid
            ? "bg-status-waiting-bg text-status-waiting-text hover:bg-status-waiting-bg/70"
            : "bg-field-fill text-foreground hover:bg-accent",
        )}
      />
    </>
  );
}
