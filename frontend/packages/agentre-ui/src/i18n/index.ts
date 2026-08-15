import { useTranslation } from "react-i18next";

import en from "./locales/en.json";
import zhCN from "./locales/zh-CN.json";

/**
 * 包的 i18n **只出资源，不建实例**。
 *
 * 建第二个 i18next 实例等于第二套语言状态：宿主切到 en，包内还留在 zh-CN，
 * 而且 `react-i18next` 的 context 也会分叉（`useTranslation` 会拿到未初始化的
 * 那个实例）。所以 i18next / react-i18next 都是 peerDependency，实例由宿主持有，
 * 包只把自己的语言包交出去让宿主在 init 时并进去。
 *
 * ### 为什么是独立 namespace 而不是继续用 `common`
 *
 * 共用 `common` 的话，宿主 3410 行的 key 与包的 key 落在同一棵树上，谁后 merge
 * 谁赢——覆盖是**静默**的（i18next 不报重复 key）。要挡住就得再约定一层 key 前缀
 * （`agentreUi.codeBlock.copyDone`），那意味着搬迁 47 个组件时每个 `t("...")`
 * 字符串都要改写。
 *
 * 独立 namespace 两头都省：
 *   - 覆盖在结构上不可能——两棵树本来就不在一个 bundle 里；
 *   - key 路径原样保留（`codeBlock.copyDone` 还是 `codeBlock.copyDone`），
 *     搬迁时把宿主 common.json 的整棵子树剪进来即可，组件里的 key 一个不用改，
 *     只把 `useTranslation()` 换成 `useUiTranslation()`。
 */
export const AGENTRE_UI_NAMESPACE = "agentreUi";

/**
 * 宿主在 `i18n.init({ resources })` 里把它挂到 `AGENTRE_UI_NAMESPACE` 下；
 * 已经 init 过的宿主也可以走 `i18n.addResourceBundle(lng, ns, bundle)`。
 * 语言键与宿主的 `supportedLanguages` 对齐。
 *
 * 语言包用 `.json` 而不是 `.ts`，是为了让 47 个组件搬迁时能把宿主
 * `common.json` 的子树整块剪过来。代价：`tsc` 产出的 dist 里是不带
 * `with { type: "json" }` 的裸 JSON import——消费方必须过打包器
 * （Vite/Rollup/webpack 都支持），裸 Node ESM 直接 import dist 会报
 * ERR_IMPORT_ATTRIBUTE_MISSING。本包是浏览器 UI 层，这个前提成立。
 */
export const agentreUiResources = {
  "zh-CN": zhCN,
  en,
};

/**
 * 包内组件**只准**用这个入口取 `t`，不要裸调 `useTranslation()`。
 *
 * 裸调会落到宿主的 defaultNS（`common`）上：搬迁过程中宿主那份 key 还在，
 * 于是它看起来是好的；等宿主把 key 删掉，文案才在用户面前变成 key 字符串。
 * 收敛到一个入口，是让这类失败在搬迁当天就暴露。
 */
export function useUiTranslation() {
  return useTranslation(AGENTRE_UI_NAMESPACE);
}
