import { resolveModelBrand } from "../ai-brand-logo";
import { modelDisplayName } from "../model-display-name";
import type { ProviderPillState } from "./provider-pill-trigger";
import type { ModelTarget, PickerProvider } from "./types";

/**
 * 「这条会话的模型 pill 此刻是哪一态、脸上写什么」——由绑定值、生效目标与目录
 * 三样纯推出来。
 *
 * 收进包里是因为它本来就是 `ProviderPillTrigger` 那两格的入参算法，而算法与被它
 * 喂的呈现件分居两地时会漂：桌面端与 agentre-server 此前各有一份，脸上一个写
 * modelId、一个写模型人读名，失效判定一个认停用、一个只认缺失。类型早在包里
 * （`ProviderPillState`），推导不在，等于把一个概念劈成两半。
 *
 * 纯函数，不碰任何宿主设施：目录怎么来（Wails 绑定还是 REST）由宿主自己解决，
 * 这里只认 `PickerProvider`。
 */
export interface ProviderPillStateInput {
  /**
   * Agent 后端绑定的 provider key。
   *
   * 空串 = **确知没绑**，下一轮由 CLI 自身的登录账号决定模型；`undefined` / `null`
   * = 还没拿到。两者绝不能混：拿后者当前者，加载中的一瞬会闪成「CLI 登录态」。
   */
  boundProviderKey?: string | null;
  /**
   * 绑定的 model key。`undefined` = 还不知道（新建会话的 Agent 条目就没有这一格）：
   * 此时即使目录里看得见默认模型，也不能断言它绑的是 provider-default —— 它也可能
   * 固定到了另一个模型。空串 / `null` = 确知跟随该供应商当前默认。
   */
  boundModelKey?: string | null;
  /** 这条会话生效的目标。两格皆空 = 跟随 Agent 绑定。 */
  target: ModelTarget;
  catalog: PickerProvider[];
  /**
   * 这一轮执行的 CLI 后端类型，只在跟随态图标的回落链第三格（后端标识）用得到。
   * 宿主不知道就不传——视图模型上它是可选字段。
   */
  backendType?: string | null;
}

/**
 * 「跟随 Agent 绑定」那一格该画哪个标识：模型 → 供应商 → 后端 → 人形（spec 决策 8）。
 *
 * 品牌标识比人形更快认出模型，所以模型 ID 认得出品牌就用模型的；认不出（中转商的
 * 私有化命名）退到供应商标识（供应商的三种 type 都有真品牌 mark），连供应商都没有
 * （CLI 登录态 / 还没解析）则退到后端标识，最次回到人形——**绝不空出一格**。
 *
 * 收在这里而不是内联在触发器里，是因为它是「脸」的语义；宿主不该为同一颗 pill 各写一份。
 */
export type ProviderPillLogoTier = "model" | "provider" | "backend" | "person";

export function resolveProviderPillLogoTier(input: {
  /** 解析到的模型 ID；展示名猜不出品牌，所以只认它。 */
  modelId: string;
  providerType: string;
  /** 这一轮执行的 CLI 后端类型；空串 = 宿主还没告诉。 */
  backendType?: string | null;
}): ProviderPillLogoTier {
  if (input.modelId && resolveModelBrand(input.modelId)) return "model";
  if (input.providerType) return "provider";
  if (input.backendType) return "backend";
  return "person";
}

/** 钉了目标但它在目录里解析不出来（缺失 / 停用 / 被删）。未钉恒为假。 */
function targetInvalid(
  target: ModelTarget,
  catalog: PickerProvider[],
): boolean {
  if (target.providerKey === "" && target.modelKey === "") return false;
  const p = catalog.find((x) => x.providerKey === target.providerKey);
  if (!p || !p.enabled) return true;
  // provider-default：只要供应商还在且可用就成立，默认模型解析不到只是脸上少一格。
  if (target.modelKey === "") return false;
  const m = p.models.find((x) => x.modelKey === target.modelKey);
  return !m || !m.enabled;
}

/** 跟随 Agent 绑定那一态：脸上写的是**绑定值此刻解析到的东西**。 */
function followAgentState(input: ProviderPillStateInput): ProviderPillState {
  const { boundProviderKey, boundModelKey, catalog } = input;
  const provider = catalog.find((c) => c.providerKey === boundProviderKey);
  const providerLabel = provider?.name ?? boundProviderKey ?? "";
  const providerType = provider?.type ?? "";
  const cliLogin = boundProviderKey === "";

  if (boundModelKey === undefined) {
    return {
      mode: "follow-agent",
      providerLabel,
      providerType,
      modelLabel: "",
      modelId: "",
      resolutionLabel: providerLabel,
      dynamic: false,
      cliLogin,
      backendType: input.backendType ?? "",
    };
  }

  const fixedModel = boundModelKey
    ? provider?.models.find((m) => m.modelKey === boundModelKey)
    : undefined;
  const resolvedModel = fixedModel ?? provider?.defaultModel ?? undefined;
  const modelLabel = resolvedModel ? modelDisplayName(resolvedModel) : "";
  return {
    mode: "follow-agent",
    providerLabel,
    providerType,
    modelLabel,
    modelId: resolvedModel?.modelId ?? "",
    resolutionLabel: modelLabel
      ? `${providerLabel} · ${modelLabel}`
      : providerLabel,
    // 绑的是供应商而不是具体模型 → 供应商换默认模型这里就跟着换，那正是 ↻。
    dynamic: !fixedModel && !!resolvedModel,
    cliLogin,
    backendType: input.backendType ?? "",
  };
}

export function resolveProviderPillState(
  input: ProviderPillStateInput,
): ProviderPillState {
  const { target, catalog } = input;
  if (target.providerKey === "" && target.modelKey === "") {
    return followAgentState(input);
  }

  const provider = catalog.find((c) => c.providerKey === target.providerKey);
  const providerLabel = provider?.name ?? target.providerKey;
  const providerType = provider?.type ?? "";
  const invalid = targetInvalid(target, catalog);
  const selectedModel = target.modelKey
    ? provider?.models.find((m) => m.modelKey === target.modelKey)
    : (provider?.defaultModel ?? undefined);
  // 解析不到就退回原始 key：失效态下用户要认的正是「我钉的那个东西没了」。
  const modelLabel = selectedModel
    ? modelDisplayName(selectedModel)
    : target.modelKey;
  return {
    mode: invalid ? "invalid" : target.modelKey ? "fixed" : "provider-default",
    providerLabel,
    providerType,
    modelLabel,
    // 原始 key 不是模型 ID，不能交给品牌标识去猜。
    modelId: selectedModel?.modelId ?? "",
    resolutionLabel: modelLabel
      ? `${providerLabel} · ${modelLabel}`
      : providerLabel,
    dynamic: !invalid && target.modelKey === "" && !!selectedModel,
    // 会话自己选了目标，就不再由 CLI 登录账号决定。
    cliLogin: false,
  };
}
