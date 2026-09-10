import i18n from "@/i18n";

// isChatSteerNoActiveError 用 i18n 文案前缀匹配。Wails 把 service 端的
// i18n.NewError 透传成普通 Error，没有结构化 code，只能按字串识别。
function isChatSteerNoActiveError(msg: string): boolean {
  return (
    msg.includes(
      i18n.t("chatPanel.errors.noActiveConversation", { lng: "zh-CN" }),
    ) ||
    msg.includes(i18n.t("chatPanel.errors.noActiveConversation", { lng: "en" }))
  );
}

// isChatStopNoActiveError 同上：后端 ChatStopNoActive 错误码的中英文文案。
// Stop 与 turn 自然完成发生 race 时（用户点击之后、后端已自清 activeCancels）
// 会返这条；属于无害的「太晚了」，UI 不弹错。
function isChatStopNoActiveError(msg: string): boolean {
  return (
    msg.includes(
      i18n.t("chatPanel.errors.noActiveTurnToStop", { lng: "zh-CN" }),
    ) ||
    msg.includes(i18n.t("chatPanel.errors.noActiveTurnToStop", { lng: "en" }))
  );
}

function isExactCompactCommand(text: string): boolean {
  return text.trim() === "/compact";
}

function isExactNewCommand(text: string): boolean {
  return text.trim() === "/new";
}

type GoalCommand =
  | { kind: "get" }
  | { kind: "clear" }
  | { kind: "set"; objective: string }
  | { kind: "status"; status: "active" | "paused" | "complete" };

function parseGoalCommand(text: string): GoalCommand | null {
  const trimmed = text.trim();
  if (trimmed === "/goal") return { kind: "get" };
  if (!trimmed.startsWith("/goal ")) return null;
  const arg = trimmed.slice("/goal ".length).trim();
  if (!arg) return { kind: "get" };
  switch (arg) {
    case "clear":
      return { kind: "clear" };
    case "pause":
      return { kind: "status", status: "paused" };
    case "resume":
      return { kind: "status", status: "active" };
    case "complete":
      return { kind: "status", status: "complete" };
    default:
      return { kind: "set", objective: arg };
  }
}

export {
  isChatSteerNoActiveError,
  isChatStopNoActiveError,
  isExactCompactCommand,
  isExactNewCommand,
  parseGoalCommand,
};
export type { GoalCommand };
