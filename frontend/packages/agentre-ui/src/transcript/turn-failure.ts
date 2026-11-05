import type { TFunction } from "i18next";

/**
 * 一轮失败的原因类别 —— 决定失败卡说哪句话、给哪个出口。
 *
 * - `overloaded` / `rateLimited` / `network`：瞬态，原样重试同一条消息就可能成。
 * - `authExpired`：CLI 登录失效，只有在终端重新登录才修得好，重试必然再失败。
 * - `processExited`：agent 进程中途退出或这一轮被打断，接着做（继续）或重来。
 * - `unknown`：分不出类。
 */
export type TurnFailureKind =
  | "overloaded"
  | "rateLimited"
  | "authExpired"
  | "processExited"
  | "network"
  | "unknown";

// errorText 的源头是后端落库的 stopErr.Error() —— 各家 CLI 的输出串，或远端中断/
// 断连时 code 包里的中英文案。后端不带结构化类别（加类别要成对改 wire），所以这里
// 是**唯一**的匹配点：失败卡、出错通知都经 classifyTurnFailure 取类别，别处不再
// 各自匹配字符串。
//
// 按顺序取第一条命中：错误串常常层层包裹（「进程退出: API Error: 529 …」），越具体、
// 越决定出口的类别越靠前 —— 登录失效排最前，因为它在任何包裹下都只能靠重新登录修。
const RULES: ReadonlyArray<readonly [TurnFailureKind, RegExp]> = [
  [
    "authExpired",
    /authentication_error|authentication_failed|failed to authenticate|oauth token (?:has )?expired|invalid api key|please run \/login|not logged in|refresh token has expired|\b401 unauthorized\b/i,
  ],
  [
    "rateLimited",
    /rate_limit_error|rate[ _-]?limit|usage limit|too many requests|\b(?:api error|status|http)[:\s]+429\b/i,
  ],
  ["overloaded", /overloaded|\b(?:api error|status|http)[:\s]+529\b/i],
  [
    "network",
    /connection (?:closed|refused|reset)|econn(?:reset|refused)|enotfound|etimedout|socket hang up|fetch failed|request timed out|dial tcp|no such host|i\/o timeout|network is unreachable|stream disconnected|agentred is unreachable|连不上/i,
  ],
  [
    "processExited",
    /process exited|subprocess exited|exited on startup|\bexit(?: status)? \d+|signal: (?:killed|terminated)|run interrupted|was interrupted|被打断/i,
  ],
];

/** 把一轮失败的 errorText 归到 {@link TurnFailureKind}；空串与分不出的一律 `unknown`。 */
export function classifyTurnFailure(errorText: string): TurnFailureKind {
  for (const [kind, pattern] of RULES) {
    if (pattern.test(errorText)) return kind;
  }
  return "unknown";
}

/**
 * 每一类失败的那句中文事实（en 界面是英文同义句），失败卡标题与出错通知共用。
 *
 * `t` 必须绑在包的 namespace 上：组件里用 `useUiTranslation().t`，宿主在组件树
 * 外取文案时用 `i18n.getFixedT(null, AGENTRE_UI_NAMESPACE)`。
 */
export function turnFailureFact(kind: TurnFailureKind, t: TFunction): string {
  switch (kind) {
    case "overloaded":
      return t("chat.errorCard.fact.overloaded");
    case "rateLimited":
      return t("chat.errorCard.fact.rateLimited");
    case "authExpired":
      return t("chat.errorCard.fact.authExpired");
    case "processExited":
      return t("chat.errorCard.fact.processExited");
    case "network":
      return t("chat.errorCard.fact.network");
    case "unknown":
      return t("chat.errorCard.fact.unknown");
  }
}

// 登录命令按后端类型给；不在表里的后端没有确定能走通的命令，失败卡就不给命令卡。
const LOGIN_COMMANDS: ReadonlyMap<string, string> = new Map([
  ["claudecode", "claude /login"],
  ["codex", "codex login"],
]);

/** 该后端重新登录的终端命令；未知后端返回 undefined。 */
export function loginCommandFor(
  backendType: string | undefined,
): string | undefined {
  return backendType ? LOGIN_COMMANDS.get(backendType) : undefined;
}
