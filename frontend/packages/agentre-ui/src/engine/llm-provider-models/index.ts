import { llm_provider_svc } from "../port-bridge";

export type Provider = llm_provider_svc.ProviderItem;
export type Model = llm_provider_svc.ModelItem;
export type ModelInfo = llm_provider_svc.ModelInfo;
export type ReferenceCounts = llm_provider_svc.ReferenceCounts;

export type ProviderType = "anthropic" | "openai-chat" | "openai-response";

export type ProviderTypeMeta = {
  badge: string;
  defaultBaseUrl: string;
};

export const providerTypeMeta: Record<ProviderType, ProviderTypeMeta> = {
  anthropic: {
    badge: "A",
    defaultBaseUrl: "https://api.anthropic.com",
  },
  "openai-chat": {
    // 两个 openai 变体首字母都是 O，用 OC/OR 区分。
    badge: "OC",
    defaultBaseUrl: "https://api.openai.com/v1",
  },
  "openai-response": {
    badge: "OR",
    defaultBaseUrl: "https://api.openai.com/v1",
  },
};

export const providerTypeOrder: ProviderType[] = [
  "anthropic",
  "openai-response",
  "openai-chat",
];

export function isProviderType(value: string): value is ProviderType {
  return value in providerTypeMeta;
}

export function errMessage(err: unknown): string {
  if (err instanceof Error) return err.message;
  if (typeof err === "string") return err;
  return "Unknown error";
}

export function formatTokens(n: number): string {
  if (n >= 1_000_000) {
    return `${(n / 1_000_000).toFixed(n % 1_000_000 === 0 ? 0 : 1)}M`;
  }
  if (n >= 1_000) {
    return `${(n / 1_000).toFixed(n % 1_000 === 0 ? 0 : 1)}K`;
  }
  return n > 0 ? String(n) : "—";
}

export function totalReferences(
  counts: ReferenceCounts | null | undefined,
): number {
  if (!counts) return 0;
  return counts.backends + counts.sessions + counts.routes;
}

export type ModelDeleteability = { kind: "ok" } | { kind: "default" };

// 唯一的「可否删除」判定来源：只有默认模型不可删——那是 Provider 自身的不变量
// （删了它，该 Provider 下所有 provider-default 目标都解析不出模型）。被引用不再
// 阻止删除，引用数只作为删除前的知情材料；引用数没加载出来同样不阻止删除。
// 模型表行内标注与批量删除确认分组共用。
export function modelDeleteability(
  model: Model,
  defaultModelKey: string,
  _modelRefCounts: Map<string, ReferenceCounts>,
): ModelDeleteability {
  if (model.modelKey === defaultModelKey) return { kind: "default" };
  return { kind: "ok" };
}

export function endpointFor(provider: Provider): string {
  const providerType = provider.type as ProviderType;
  const meta =
    providerType in providerTypeMeta
      ? providerTypeMeta[providerType]
      : undefined;
  return provider.baseUrl || meta?.defaultBaseUrl || "—";
}
