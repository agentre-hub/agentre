// 保存前的纯校验：主绑定目标解析、piagent 的模型必达、OpenClaw 草稿体检，
// 最后收敛成一句「为什么现在不能保存」。这条链既喂 Summary 的提示，也决定
// 保存按钮的禁用，所以只算一次。
import { resolveModelTarget } from "../agent-backends-utils";
import type { PickerProvider } from "../model-target-picker";
import { OPENCLAW_SESSION_MODE } from "../openclaw-backend-fields";
import { openClawDraftIssue } from "../openclaw-validation";
import {
  isCliBackend,
  type BackendType,
  type Provider,
  type Translate,
} from "../agent-backends-shared";

import { openClawProbeErrorMessage } from "./draft";

export type SaveValidation = ReturnType<typeof computeSaveValidation>;

export function computeSaveValidation(args: {
  type: BackendType;
  name: string;
  // llmProviderKey 传 effectiveLlmProviderKey（含 builtin 的自动选中）。
  llmProviderKey: string;
  llmModelKey: string;
  openClawGatewayURL: string;
  targetCatalog: PickerProvider[];
  filteredProviders: Provider[];
  reservedOffenders: string[];
  t: Translate;
}) {
  const {
    type,
    name,
    llmProviderKey,
    llmModelKey,
    targetCatalog,
    filteredProviders,
    reservedOffenders,
    t,
  } = args;
  // builtin 必须有 provider；CLI 自身登录、OpenClaw 走 Gateway 认证，都允许未关联。
  const providerOptional = isCliBackend(type) || type === "openclaw";
  // piagent 绑定时 provider-default / fixed-model 都必须最终解析到可用模型
  // （spec「ModelTarget contract」）。目录加载完成且能确定目标解析不到模型时才前置拦截。
  const selectedTargetProvider = targetCatalog.find(
    (p) => p.providerKey === llmProviderKey,
  );
  const piAgentModelMissing =
    type === "piagent" &&
    llmProviderKey !== "" &&
    !!selectedTargetProvider &&
    (llmModelKey !== ""
      ? !selectedTargetProvider.models.some(
          (m) => m.modelKey === llmModelKey && m.enabled,
        )
      : !selectedTargetProvider.defaultModel);
  // 主目标是否已失效：绑定了 provider/model 但目录里解析不出来（Provider/Model 缺失/停用）。
  const mainTargetInvalid =
    llmProviderKey !== "" &&
    (!selectedTargetProvider ||
      !selectedTargetProvider.enabled ||
      (llmModelKey !== ""
        ? !selectedTargetProvider.models.some(
            (m) => m.modelKey === llmModelKey && m.enabled,
          )
        : !selectedTargetProvider.defaultModel?.enabled));
  const resolvedMainTarget = resolveModelTarget(
    llmProviderKey,
    llmModelKey,
    targetCatalog,
  );
  const openClawIssue =
    type === "openclaw"
      ? openClawDraftIssue({
          name,
          gatewayURL: args.openClawGatewayURL,
          sessionMode: OPENCLAW_SESSION_MODE,
        })
      : null;
  const saveBlockedReason =
    name.trim() === ""
      ? t("agentBackends.summary.reasons.nameRequired")
      : mainTargetInvalid
        ? t("agentBackends.summary.reasons.invalidTarget")
        : piAgentModelMissing
          ? t("agentBackends.provider.modelRequiredTitle")
          : !providerOptional &&
              (filteredProviders.length === 0 || llmProviderKey === "")
            ? t("agentBackends.summary.reasons.bindingRequired")
            : isCliBackend(type) && reservedOffenders.length > 0
              ? t("agentBackends.env.reservedDisabled", {
                  keys: reservedOffenders.join(", "),
                })
              : openClawIssue
                ? openClawProbeErrorMessage(openClawIssue, "", t)
                : null;
  const effectiveSaveBlockedReason =
    type !== "openclaw" && resolvedMainTarget.mode === "invalid"
      ? t("agentBackends.summary.reasons.invalidTarget")
      : saveBlockedReason;
  return {
    piAgentModelMissing,
    mainTargetInvalid,
    resolvedMainTarget,
    effectiveSaveBlockedReason,
  };
}
