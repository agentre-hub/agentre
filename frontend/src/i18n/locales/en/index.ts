// 文案仍然只有 `common` 这一个 namespace —— 下面的模块文件是**物理**切分,不是
// i18next 意义上的 ns,所以 key 里没有模块名这一段:`chat.json` 里的文案照旧写
// `t("chatPanel.title")`。分文件只为让一棵 3000 行的树按功能域可读、可并行改。
//
// 新增一个模块文件必须同时在这里 import 进来,漏掉不会报错、只会让整块文案静默
// 消失(回退到 key 原文)。src/__tests__/i18n-locale-modules.test.ts 守着这件事,
// 同时守顶层 key 不被两个模块同时认领(后 spread 的那个会静默覆盖前一个)。
import agents from "./agents.json";
import chat from "./chat.json";
import common from "./common.json";
import hooks from "./hooks.json";
import llm from "./llm.json";
import org from "./org.json";
import projects from "./projects.json";
import remote from "./remote.json";
import session from "./session.json";
import settings from "./settings.json";

export default {
  ...agents,
  ...chat,
  ...common,
  ...hooks,
  ...llm,
  ...org,
  ...projects,
  ...remote,
  ...session,
  ...settings,
};
