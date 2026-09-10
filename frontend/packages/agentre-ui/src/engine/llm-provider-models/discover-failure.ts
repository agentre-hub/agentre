/**
 * 发现模型这条路上的两件纯事：模型族的可读名，与上游错误的归类。
 *
 * 归类是为了让主文案说人话——「认证失败（401）」而不是把 Go 那侧的错误串
 * 直接摊在对话框中央。原始文本只进折叠区，一个字都不丢，但不占主位。
 */
import type { ModelInfo } from "./index";
import { errMessage } from "./index";

// 模型族可读标签：内置目录 vendor 为小写标识，映射为品牌名；未收录的 vendor
// 兜底为大小写可读形式，认不出的（空 vendor）由调用方归入「其它」分组。
const VENDOR_LABELS: Record<string, string> = {
  anthropic: "Anthropic",
  deepseek: "DeepSeek",
  gemini: "Gemini",
  glm: "GLM",
  kimi: "Kimi",
  meta: "Meta",
  minimax: "MiniMax",
  mistral: "Mistral AI",
  openai: "OpenAI",
  qwen: "Qwen",
  xai: "xAI",
};

export function vendorLabel(vendor: string): string {
  if (!vendor) return "";
  return (
    VENDOR_LABELS[vendor] ?? vendor.charAt(0).toUpperCase() + vendor.slice(1)
  );
}

export type FailureKind =
  | "auth"
  | "endpoint"
  | "server"
  | "status"
  | "network"
  | "generic";

// 把上游错误归类成可理解的失败原因；原始文本只用于折叠区，不作为主文案。
// 状态码一路带到标题里（「认证失败（401）」），不要在归类时丢掉。
export function discoverFailure(err: unknown): {
  kind: FailureKind;
  code?: string;
} {
  const msg = errMessage(err);
  const status = msg.match(/http\s+(\d{3})/i)?.[1];
  if (status) {
    const code = Number(status);
    if (code === 401 || code === 403) return { kind: "auth", code: status };
    if (code === 404) return { kind: "endpoint", code: status };
    if (code >= 500) return { kind: "server", code: status };
    return { kind: "status", code: status };
  }
  if (
    /(?:connection refused|no such host|getaddrinfo|timed out|timeout|econnrefused|enotfound|dial tcp|network)/i.test(
      msg,
    )
  ) {
    return { kind: "network" };
  }
  return { kind: "generic" };
}

export type VendorGroup = { key: string; label: string; items: ModelInfo[] };
