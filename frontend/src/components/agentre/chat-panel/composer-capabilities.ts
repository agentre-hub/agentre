// Composer 控件可见性的单一判定入口：从 runtime 能力矩阵（caps）收敛出
// 「这一轮该摆哪些控件」。把判定集中在这里，是为了让新增后端（Hermes 只声明
// abort）的事实有唯一的消费点，而不是散在 chat-panel 的各个 JSX 条件里。
import type { Capabilities } from "../capability/types";

export type ComposerCapabilities = {
  /** 生成中能否把新消息插进当前 turn（steer）；false 时不排队、不摆排队条。 */
  canSteer: boolean;
  /** permission mode pill / Shift+Tab 循环。 */
  canSetPermissionMode: boolean;
  /** 反向提问（AskUserQuestion 卡片）的可答能力。 */
  canAnswerUserAsk: boolean;
  supportsReasoningEffort: boolean;
  supportsImageInput: boolean;
  canStopBackgroundTask: boolean;
  /** `/compact` 的宿主压缩通路。 */
  supportsCompact: boolean;
};

/**
 * deriveComposerCapabilities 把 caps 翻成 booleans。
 *
 * caps 未到位（null/undefined）时不能凭空断言「不支持」：steer 与 compact 沿用既有
 * 行为（caps 到达前照旧走排队 / codex·piagent 自家压缩通路），其余控件保守关闭，
 * 避免在能力未知时摆出一个点了没反应的按钮。
 */
export function deriveComposerCapabilities(
  caps: Capabilities | null | undefined,
  fallbackBackendType?: string | null,
): ComposerCapabilities {
  return {
    // 未知 → 不拦（既有行为就是排队，后端若不支持会自己拒）；确知不支持才关。
    canSteer: caps ? caps.has("steer") : true,
    canSetPermissionMode: !!caps?.has("set_permission_mode"),
    canAnswerUserAsk: !!caps?.has("answer_user_ask"),
    supportsReasoningEffort: !!caps?.has("reasoning_effort"),
    supportsImageInput: !!caps?.has("image_input"),
    canStopBackgroundTask: !!caps?.has("stop_background_task"),
    supportsCompact: caps
      ? caps.has("compact")
      : fallbackBackendType === "codex" || fallbackBackendType === "piagent",
  };
}
