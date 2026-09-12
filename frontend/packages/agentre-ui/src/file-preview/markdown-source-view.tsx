import { CodePreview, type CodePreviewProps } from "./code-view";

// markdown 源码只读档：走与代码/文本完全相同的 Monaco 路径（spec 决策 7），
// 显式锁 markdown 语言做源码语法着色；绝不 GFM 渲染 —— 渲染档由 MarkdownText
// 承担，这里只负责「看原始 markdown 源码」。
export function MarkdownSourceView(props: Omit<CodePreviewProps, "language">) {
  return <CodePreview {...props} language="markdown" />;
}
