/**
 * 原生表单控件守卫的规则数据。
 *
 * 「表单控件统一用 shadcn `@/components/ui/*` / 共享包」这条写在 AGENTS.md 里很久，
 * 但一直只是一句话。代价在 2026-08-23 结清：共享包里当时没有 Select / Checkbox，
 * 搬进去的项目设置就地退回了系统控件（父项目一颗原生 `<select>`、「显示隐藏项」一颗
 * 原生复选框），agentre-server 的组织面跟着照做，六处搜索框各手搓了一遍。系统控件
 * 走浏览器自己的配色，与 tokens.css 那张表无关 —— 深色下最先露馅的就是它们。
 *
 * 与设计 token 那组共用同一条 `no-restricted-syntax`：规则数据单独成模块，守卫测试
 * 与 eslint.config.js 消费同一份来源（见 src/__tests__/eslint-native-controls.test.ts）。
 *
 * **豁免只有两处**，都在 eslint.config.js 里写明：
 *   - `packages/agentre-ui/src/ui/**`：原语自己的实现，原生标签总得有一处住处；
 *   - `<input type="file">`：没有可替代的原语形态，它总是藏起来由一颗按钮触发。
 *     这一条由选择器本身表达（只点名 checkbox / radio / search），不靠文件豁免。
 */

const HINT =
  "表单控件一律走共享包 @agentre-hub/agentre-ui（宿主可继续从 @/components/ui/* 取转发）：" +
  " Select / Checkbox / Input / Textarea / SearchInput。原生控件由浏览器自己上色，" +
  " 与 tokens.css 无关，深色主题下必然对不上；缺哪个原语就先往包里补哪个，不要就地退让。";

/** `<input type="…">` 里这几种都有对应的原语。 */
const REPLACEABLE_INPUT_TYPES = ["checkbox", "radio", "search"];

const nativeControlSyntax = [
  {
    selector: 'JSXOpeningElement[name.name="select"]',
    message: `禁止原生 <select>。${HINT}`,
  },
  {
    selector: 'JSXOpeningElement[name.name="textarea"]',
    message: `禁止原生 <textarea>。${HINT}`,
  },
  ...REPLACEABLE_INPUT_TYPES.map((type) => ({
    selector: `JSXOpeningElement[name.name="input"] > JSXAttribute[name.name="type"][value.value="${type}"]`,
    message: `禁止原生 <input type="${type}">。${HINT}`,
  })),
];

export { HINT, REPLACEABLE_INPUT_TYPES, nativeControlSyntax };
