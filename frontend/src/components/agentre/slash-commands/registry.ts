// 桌面端**自己**那几条 `/` 命令。
//
// 两端共同的清单(`/compact`、`/goal`、Skill 目录翻译、按 backend 过滤)已经收进
// 共享包 `@agentre-hub/agentre-ui` 的 chat-input/slash/registry —— 此前这里与
// 服务端 `lib/slashCommands.ts` 是逐条对照抄出来的两份。
//
// 留在这里的只有一条,判据是它**做不到跨宿主**:`/new` 是开一个新标签页,浏览器
// 那一端没有标签页这回事,摆过去会是一颗按下去什么也不发生的命令。
import i18n from "@/i18n";

import type { SlashCommand } from "@agentre-hub/agentre-ui";

// 纯前端 tab 操作:Enter 时由 chat-panel 拦截恰为 `/new` 的文本,沿用当前会话的
// agent / 项目开一个全新空白会话 tab 并跳转。与 backend 无关,故所有非空 backend
// 都可用。
export const desktopSlashCommands: SlashCommand[] = [
  {
    name: "new",
    label: "/new",
    trigger: "/",
    get description() {
      // 取值推迟到读的那一刻:模块加载时就求值会把文案冻结在 import 那一刻的语言上
      // (切了语言也不变)。包里那几条走 `useSlashCommands` 的 `t`,天然跟着语言走;
      // 这一条不经过 hook,所以在这里自己推迟。
      return i18n.t("slashCommands.new.description");
    },
    resolve(backend) {
      return backend ? { kind: "literal_text", text: "/new" } : null;
    },
  },
];
