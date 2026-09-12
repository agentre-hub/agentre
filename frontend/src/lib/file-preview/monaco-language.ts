// 扩展名 / 文件名 → Monaco 语言 id 的映射已经搬进共享包：它是 CodePreview /
// DiffPreview 语言判定的一部分，两端共用同一份（packages/agentre-ui/src/
// file-preview/monaco-language.ts）。这里只做转发，宿主既有调用点（file-type-icon
// 的语言角标）不必各自改说明符。
export { monacoLanguageForPath } from "@agentre-hub/agentre-ui";
