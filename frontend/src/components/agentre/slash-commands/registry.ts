// Slash command 注册表。新增命令时只需在 slashCommands 数组里追加;按 backend 分发
// 执行策略由 SlashCommand.resolve(backendType) 决定 —— 返回 null 表示该命令在此
// backend 不可用,UI 直接从候选列表里过滤掉。
//
// 已知后端:
//   - claudecode: CLI 自己识别 / 前缀,绝大多数命令都走 literal_text(把命令字符串
//     当普通 user 文本送过去 SendChatMessage)。
//   - codex / piagent: chat-panel 的 onSubmit 会拦截 `/compact` 文本转走
//     CompactChatSession RPC —— 所以这里也只需要 literal_text,用户从菜单选中
//     只补全文字,真正分发由 Enter 走 chat-panel 完成。
//   - builtin / 其它: 暂不参与 slash 命令。

// 命令的**数据契约**在共享包里（`SlashCommand` / `SlashExec`），**清单在这里**：
// 文案要读宿主的 i18next 实例、`/new` 是纯桌面的开标签页动作、skill 目录要走
// Wails 绑定拉 —— 这些都进不了包。理由见包内 chat-input/slash/types.ts。
import type { SlashCommand } from "@agentre-ai/agentre-ui";

import i18n from "@/i18n";

export const slashCommands: SlashCommand[] = [
  {
    name: "compact",
    label: "/compact",
    trigger: "/",
    description: i18n.t("slashCommands.compact.description"),
    resolve(backend) {
      if (
        backend === "claudecode" ||
        backend === "codex" ||
        backend === "piagent"
      ) {
        return { kind: "literal_text", text: "/compact" };
      }
      return null;
    },
  },
  {
    name: "goal",
    label: "/goal",
    trigger: "/",
    description: i18n.t("slashCommands.goal.description"),
    resolve(backend) {
      if (backend === "codex") {
        return { kind: "literal_text", text: "/goal " };
      }
      return null;
    },
  },
  {
    // 纯前端 tab 操作:Enter 时由 chat-panel 拦截恰为 `/new` 的文本,沿用当前会话的
    // agent / 项目开一个全新空白会话 tab 并跳转。与 backend 无关,故所有非空 backend 都可用。
    name: "new",
    label: "/new",
    trigger: "/",
    description: i18n.t("slashCommands.new.description"),
    resolve(backend) {
      return backend ? { kind: "literal_text", text: "/new" } : null;
    },
  },
];

export type SkillCommandSource = {
  name: string;
  description?: string;
};

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

// skillCommandsFromCatalog 把后端已解析的裸 Skill 名称映射为输入命令。
// service 返回的名称不带触发前缀；这里也容忍带前缀的输入，确保 UI 只添加一次。
export function skillCommandsFromCatalog(
  backendType: string,
  catalog: SkillCommandSource[],
): SlashCommand[] {
  const trigger = skillCommandPrefix(backendType);
  if (!trigger) return [];

  const fallbackDescription = i18n.t(
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
      // 标成 Skill:claudecode / Pi 的 Skill 也走 /,与内置命令同一个触发键,
      // 包里靠 trigger 分不出来。占位文案要据此说「/ 触发命令和 Skill」。
      kind: "skill",
      description: source.description?.trim() || fallbackDescription,
      resolve(currentBackend) {
        return currentBackend === backendType
          ? { kind: "literal_text", text: label }
          : null;
      },
    });
  }
  return commands;
}

// listAvailable 返回当前 backend 下可用的命令清单。UI 用它做下拉候选。
export function listAvailable(
  backendType: string,
  dynamicCommands: SlashCommand[] = [],
): SlashCommand[] {
  if (!backendType) return [];
  const seen = new Set<string>();
  return [...slashCommands, ...dynamicCommands].filter((command) => {
    if (command.resolve(backendType) === null) return false;
    const key = `${command.trigger}:${command.name}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}
