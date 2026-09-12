/**
 * `/` 命令与 Skill 命令的**清单**。
 *
 * ### 为什么它现在住在包里
 *
 * 这个文件此前是两份:桌面端 `slash-commands/registry.ts` 与服务端
 * `lib/slashCommands.ts`,逐条对照着抄。两份的理由(写在旧 `types.ts` 里)是「文案要
 * 读宿主的 i18next 实例、skill 目录要走 Wails 绑定」——两条今天都不成立了:
 *
 *   - 文案:包有自己的 namespace(`agentreUi`),`useSlashCommands` 经
 *     `useUiTranslation` 取 `t`,不需要宿主的实例。
 *   - Skill 目录:两端都从**执行目标所在的那台机器**问(桌面端经 Wails / 中继,
 *     浏览器经中继),取数各归各的宿主,但「拿到目录之后怎么变成命令」是同一件事。
 *
 * 剩下的真差异只有一条:`/new` 是桌面端开新标签页的纯前端动作,浏览器没有标签页
 * 这回事。它作为宿主自己的一条命令从外面递进来(`listAvailable` 的 extras),不进包。
 *
 * ### 边界
 *
 * 包只管「有哪些命令、叫什么、在哪个 backend 下解析得出什么」。**选中之后干什么**
 * 仍归宿主:`literal_text` 由输入框自己填回编辑器,而 `/compact` 在非 claudecode 上
 * 要转成一次压缩调用——那条通路两端不同(桌面端在 chat-panel 拦,服务端走
 * `runtime.run` 的 compact 参数),所以这里只把它标成一段字面文本,由宿主按
 * `isNativeCompactBackend` 决定拦不拦。
 */
import * as React from "react";

import { useUiTranslation } from "../../i18n";

import type { SlashCommand } from "./types";

/** 取文案的最小形状。包内走 `useUiTranslation`,测试直接传 `key => key`。 */
export type SlashTranslate = (key: string) => string;

/** 压缩上下文这条命令的名字。宿主提交时按它拦截。 */
export const SLASH_COMPACT = "compact";

/** CLI 自己认 `/compact` 的后端。其余由宿主自己的压缩通路承担。 */
const COMPACT_NATIVE_BACKENDS = new Set(["claudecode"]);
/** 摆得出 `/compact` 这条命令的后端(认不认由上面那张表决定)。 */
const COMPACT_BACKENDS = new Set(["claudecode", "codex", "piagent"]);

/**
 * 这个 backend 的 CLI 自己认 `/compact` 吗。
 *
 * 宿主据此决定:认的就当普通消息原样送过去,不认的要把这一句拦下来转成自己的压缩
 * 调用 —— 不拦的话,用户会看见模型一本正经地回答「/compact」这四个字。
 */
export function isNativeCompactBackend(backend: string | undefined): boolean {
  return !!backend && COMPACT_NATIVE_BACKENDS.has(backend);
}

/**
 * 两端都摆得出的内置命令。
 *
 * `/new` 不在此列,理由见文件头 —— 它是桌面端的标签页动作,由宿主自己递进来。
 */
export function buildSlashCommands(t: SlashTranslate): SlashCommand[] {
  return [
    {
      name: SLASH_COMPACT,
      label: "/compact",
      trigger: "/",
      description: t("slashCommands.compact.description"),
      resolve: (backend) =>
        COMPACT_BACKENDS.has(backend)
          ? { kind: "literal_text", text: "/compact" }
          : null,
    },
    {
      name: "goal",
      label: "/goal",
      trigger: "/",
      description: t("slashCommands.goal.description"),
      // codex 专有:别的 backend 上摆出来会是一颗按下去什么也不发生的命令。
      // 尾随空格是有意的——选中之后光标停在参数位上,用户接着打目标本身。
      resolve: (backend) =>
        backend === "codex" ? { kind: "literal_text", text: "/goal " } : null,
    },
  ];
}

/**
 * 这个 backend 的 Skill 用哪个字符触发。
 *
 * Claude Code 与 Pi 的 Skill 走 `/`,与它们的内置命令**同一个键**(所以
 * `SlashCommand.kind` 那一格才有存在的必要:光看 trigger 分不出来);Codex 按它
 * 自己的 CLI 协议走 `$`。没有 skill 这一说的 backend 回 null。
 */
export function skillCommandPrefix(backendType: string): "/" | "$" | null {
  switch (backendType) {
    case "claudecode":
    case "piagent":
      return "/";
    case "codex":
      return "$";
    default:
      return null;
  }
}

/** 目录里的一条 —— 恰好是执行端答回来的那两格。 */
export type SkillCommandSource = {
  name: string;
  description?: string;
};

/**
 * 把某台机器答回来的 Skill 目录翻成可选中的命令。
 *
 * 执行端给的名字**不带触发前缀**(协议如此);这里也容忍带前缀的输入,确保前缀只
 * 加一次。重名只留先出现的那条 —— 后面那条通常是同一个 skill 从另一个作用域被
 * 解析出来的。
 */
export function skillCommandsFromCatalog(
  backendType: string,
  catalog: SkillCommandSource[],
  t: SlashTranslate,
): SlashCommand[] {
  const trigger = skillCommandPrefix(backendType);
  if (!trigger) return [];

  const fallbackDescription = t(
    backendType === "codex"
      ? "slashCommands.skill.codexDescription"
      : backendType === "piagent"
        ? "slashCommands.skill.piDescription"
        : "slashCommands.skill.claudeDescription",
  );
  const seen = new Set<string>();
  const commands: SlashCommand[] = [];
  for (const source of catalog) {
    let name = source.name.trim();
    if (name.startsWith(trigger)) name = name.slice(trigger.length).trim();
    if (!name || seen.has(name)) continue;
    seen.add(name);
    const label = `${trigger}${name}`;
    commands.push({
      name,
      label,
      trigger,
      // 标成 Skill:占位文案要据此说「/ 触发命令」还是「/ 触发命令和 Skill」。
      kind: "skill",
      description: source.description?.trim() || fallbackDescription,
      // 目录是**问某一个 backend** 拿到的:会话换了 backend 之后这些名字未必还在,
      // 解析不出来即从候选里消失,而不是留下一批叫不动的命令。
      resolve: (currentBackend) =>
        currentBackend === backendType
          ? { kind: "literal_text", text: label }
          : null,
    });
  }
  return commands;
}

/**
 * 当前 backend 下真的可用的那些命令。
 *
 * backend 还不知道(空串)时一条都不列:列出来就得先替它猜一个支持集,而猜错的那
 * 一半是按下去不起作用的命令。
 */
export function listAvailable(
  backendType: string,
  commands: SlashCommand[],
): SlashCommand[] {
  if (!backendType) return [];
  const seen = new Set<string>();
  // 顺序由调用方给定,这里只做过滤与去重:重名时**先出现的那条赢**,所以
  // 「内置命令排在 Skill 之前」这件事必须在拼清单那一步表达(见 useSlashCommands)。
  return commands.filter((command) => {
    if (command.resolve(backendType) === null) return false;
    // 触发字符与名字都相同才算重复:`/x` 与 `$x` 是两条不同的命令。
    const key = `${command.trigger}:${command.name}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export type UseSlashCommandsOptions = {
  /** 当前会话的 backend。空 = 还不知道,菜单不列任何东西。 */
  backendType?: string;
  /**
   * 这一档执行目标此刻叫得动的 Skill 目录。取数归宿主(桌面端经 Wails 或中继、
   * 浏览器经中继),因为「问哪台机器」是宿主才知道的事。
   */
  skills?: SkillCommandSource[];
  /** 宿主自己的命令(桌面端的 `/new`)。 */
  extraCommands?: SlashCommand[];
};

/** 模块级常量:写成行内 `[]` 会让每次 render 都换身份,把下面的 memo 全部作废。 */
const EMPTY_SKILLS: SkillCommandSource[] = [];
const EMPTY_COMMANDS: SlashCommand[] = [];

/**
 * 两端共用的入口:按 backend 过滤好、文案已就位的一份命令清单。
 *
 * 做成 hook 而不是模块级常量,是因为文案要跟着**当前语言**走。此前桌面端在模块
 * 加载时就把 `i18n.t(...)` 求了值,于是那几句说明冻结在 import 那一刻的语言上,
 * 用户切了语言也不会变 —— 收进包里顺带把这条改对了。
 */
export function useSlashCommands({
  backendType,
  skills = EMPTY_SKILLS,
  extraCommands = EMPTY_COMMANDS,
}: UseSlashCommandsOptions): SlashCommand[] {
  const { t } = useUiTranslation();
  return React.useMemo(() => {
    const backend = backendType ?? "";
    if (!backend) return [];
    // 顺序:内置命令 → 宿主自己的命令 → Skill。前两组条数固定且少,Skill 可以有几
    // 十条;把 Skill 排在后面,空 query 敲下 `/` 的那一刻看到的才是常用的那几条。
    // 同时这也定下了重名的归属:内置的 `/compact` 压过同名 Skill。
    return listAvailable(backend, [
      ...buildSlashCommands(t),
      ...extraCommands,
      ...skillCommandsFromCatalog(backend, skills, t),
    ]);
  }, [backendType, skills, extraCommands, t]);
}
