/**
 * 发现模型这条路上的两件纯事：模型族的可读名，与上游错误的归类。
 *
 * 归类是为了让主文案说人话——「认证失败（401）」而不是把 Go 那侧的错误串
 * 直接摊在对话框中央。原始文本只进折叠区，一个字都不丢，但不占主位。
 */
import type { ModelInfo } from "./index";
import { messageFromError } from "../agent-backends-utils";

export type FailureKind =
  | "auth"
  | "endpoint"
  | "server"
  | "status"
  | "network"
  | "generic";

// 把上游错误归类成可理解的失败原因；原始文本只用于折叠区，不作为主文案。
// 状态码一路带到标题里（「认证失败（401）」），不要在归类时丢掉。
export function discoverFailure(
  err: unknown,
  translate: (key: string) => string,
): { kind: FailureKind; code?: string } {
  const msg = messageFromError(err, translate);
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
