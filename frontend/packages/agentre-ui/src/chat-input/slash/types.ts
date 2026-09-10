/**
 * `/` 命令的**数据契约**。
 *
 * 契约在包里、**清单在宿主**：包负责触发检测、排序、高亮与弹层渲染，而「有哪些
 * 命令、它们叫什么、在哪个 backend 下可用、选中后干什么」是宿主的产品决策 ——
 * 桌面端的注册表要读宿主 i18next 实例取文案（`/compact` 的说明）、要靠 Wails 绑定
 * 拉 Skill 目录，`/new` 更是纯桌面的开新标签页动作。这些都进不了包。
 *
 * 所以 `AIChatInput` 收的是**已经由宿主按 backend 过滤好的清单**，包内不再回头
 * 解释 backendType 与命令的关系（只在 `resolve` 时把它透传回去）。
 */

export type SlashExec =
  | {
      // 直接以普通用户消息形式发送一段文本(典型例子:claudecode 的 /compact)。
      kind: "literal_text";
      text: string;
    }
  | {
      // 调用宿主的专门 RPC 路径(典型例子:codex 没有原生 /compact,
      // 需要前端自行触发一次 Compact RPC)。handler 拿到 sessionId 自行 dispatch。
      kind: "rpc";
      handler: (ctx: { sessionId: number }) => Promise<void> | void;
    };

export type SlashCommand = {
  // canonical name (kebab-case),用于稳定 key/匹配,例:"compact"。
  name: string;
  // 下拉里显示的命令字面值,通常等于 `/${name}`。
  label: string;
  // 触发字符:Claude Code/Pi Skill 和各 backend 内置命令用 /;
  // Codex Skill mention 按 CLI 协议用 $。
  trigger: "/" | "$";
  // 这一项是命令还是 Skill。省略 = 命令。
  // 触发字符区分不出来:claudecode 与 Pi 的 Skill 也走 /,与内置命令同一个触发键。
  // 唯一的用处是占位文案 —— 「/ 触发命令」还是「/ 触发命令和 Skill」,
  // 得由宿主(它才知道清单里哪些是从 Skill 目录拉来的)说了算。
  kind?: "command" | "skill";
  // 一句话说明,会在下拉项右侧 muted 显示。**已经是可读文案而不是 i18n key** ——
  // 它同时是 `filterByQuery` 的 subtitle 评分来源,拿 key 去评分等于搜不到。
  description?: string;
  // 返回当前 backend 下的执行策略;null 表示该 backend 不支持此命令。
  resolve: (backendType: string) => SlashExec | null;
};
