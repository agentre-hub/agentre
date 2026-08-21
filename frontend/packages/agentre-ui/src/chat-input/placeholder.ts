import type { MentionSources } from "./mentions/types";
import type { SlashCommand } from "./slash/types";

/**
 * 输入框的占位文案，按**本次真正接上的能力**拼。
 *
 * 原先两端各写各的，判据都是 backendType：桌面端 `chat.tsx` 四选一
 * （`chat.composer.placeholder{,Claude,Codex,Pi}`），agentre-server 又照抄一份。
 * backendType 与「宿主接没接那些能力」无关 —— `peer-panel` 传的正是
 * `backendType=""`、没接 `onCommandSubmit` 也没接 `mentionSources`，落到的
 * 默认文案却许诺 `@ / !` 三样，它一样都没接；要不是它另写一句盖过去，
 * 用户看到的就是三条按不出反应的提示。
 *
 * 所以判据换成「这次渲染真正启用了哪些触发器」，而且**落在 `AIChatInput` 自己
 * 身上** —— 四样能力启用没有，它比谁都清楚（`mentionSources` /
 * `backendType` + `onSlashSelect` / 清单里的 trigger / `onCommandSubmit`）。
 * 省略 `placeholder` 时由它自己拼，宿主少接一样能力就自动少一段，
 * overpromise 在结构上不再可能。显式传 `placeholder` 仍然由调用方说了算 ——
 * 「发送消息到远端会话…」这类**场景**信息推不出来，只能由宿主给。
 */
export interface ComposerCapabilities {
  /** @ 提及：mentionSources 非空。 */
  mentions: boolean;
  /** / 触发命令：slash 菜单已启用，且清单里有 trigger 为 "/" 的项。 */
  slashCommands: boolean;
  /** 其中有 Skill，且 Skill 也走 "/"（claudecode / Pi）。并进 `/` 那一段。 */
  slashSkills: boolean;
  /** Skill 走 "$"（Codex CLI 协议）。单列一段。 */
  dollarSkills: boolean;
  /** ! 执行终端命令：onCommandSubmit 已接。 */
  localCommands: boolean;
}

export interface ComposerCapabilityInput {
  mentionSources?: MentionSources;
  /** backendType + onSlashSelect 同时具备 —— 与 index.tsx 里 slash 菜单的启用判据同源。 */
  slashEnabled: boolean;
  slashCommands: SlashCommand[];
  /** onCommandSubmit 已接。 */
  localCommandsEnabled: boolean;
}

export function composerCapabilities(
  input: ComposerCapabilityInput,
): ComposerCapabilities {
  const { mentionSources, slashEnabled, slashCommands } = input;
  const available = slashEnabled ? slashCommands : [];

  return {
    mentions:
      !!mentionSources &&
      mentionSources.agents.length + mentionSources.projects.length > 0,
    slashCommands: available.some((c) => c.trigger === "/"),
    slashSkills: available.some((c) => c.trigger === "/" && c.kind === "skill"),
    dollarSkills: available.some((c) => c.trigger === "$"),
    localCommands: input.localCommandsEnabled,
  };
}

/** 各段之间的分隔符。 */
const SEP = " · ";

type Translate = (key: string) => string;

export function composerPlaceholder(
  caps: ComposerCapabilities,
  t: Translate,
): string {
  const parts = [t("chatInput.placeholder.base")];
  if (caps.mentions) parts.push(t("chatInput.placeholder.mention"));
  if (caps.slashCommands) {
    parts.push(
      caps.slashSkills
        ? t("chatInput.placeholder.slashWithSkill")
        : t("chatInput.placeholder.slash"),
    );
  }
  if (caps.dollarSkills) parts.push(t("chatInput.placeholder.skill"));
  if (caps.localCommands) parts.push(t("chatInput.placeholder.localCommand"));
  return parts.join(SEP);
}
