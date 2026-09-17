// 文案仍然只有 `common` 这一个 namespace —— 下面的模块文件是**物理**切分,不是
// i18next 意义上的 ns,所以 key 里没有模块名这一段:`chat.json` 里的文案照旧写
// `t("chatPanel.title")`。分文件只为让一棵 3000 行的树按功能域可读、可并行改。
//
// 模块清单由 glob 现算:新增一个 `src/i18n/locales/<lang>/*.json` 不需要再改这里,
// 因此也不会出现「文件在盘上、整块文案静默消失」那种漏注册。按路径排序后逐份合并,
// 两个模块抢同一个顶层 key 的覆盖关系仍是确定性的。
const localeModules = import.meta.glob<Record<string, unknown>>("./*.json", {
  eager: true,
  import: "default",
});

const merged: Record<string, unknown> = {};
for (const path of Object.keys(localeModules).sort()) {
  Object.assign(merged, localeModules[path]);
}

export default merged;
