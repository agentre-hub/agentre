/**
 * `/` 命令的**数据契约**。
 *
 * 契约与**清单**都在包里（清单见 `./registry.ts`）：两端摆得出的那几条命令、Skill
 * 目录怎么翻成命令、按 backend 怎么过滤，都是同一件事，此前两个宿主各抄了一份。
 *
 * 宿主只递两样它独有的东西：
 *   - **Skill 目录**——「问哪台机器要」是宿主才知道的事（桌面端经 Wails 绑定或
 *     中继，浏览器经中继）；拿到目录之后的处理归包。
 *   - **它自己的命令**——桌面端的 `/new` 是开一个新标签页，浏览器那一端没有标签页
 *     这回事，摆过去会是一颗按下去什么也不发生的命令。
 *
 * **选中之后干什么**仍归宿主：`literal_text` 由 `AIChatInput` 直接填回编辑器，
 * 而 `/compact` 在非 claudecode 上要转成一次压缩调用——那条通路两端不同。
 *
 * `AIChatInput` 收的仍是**已经过滤好的清单**（`useSlashCommands` 的产物），包内
 * 不再回头解释 backendType 与命令的关系（只在 `resolve` 时把它透传回去）。
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
