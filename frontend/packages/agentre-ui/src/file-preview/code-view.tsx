import * as React from "react";

import { monacoLanguageForPath } from "./monaco-language";
import {
  resolveMonacoTheme,
  useMonacoThemeSync,
  type MonacoCodeEditor,
  type MonacoNS,
} from "./monaco";

export type CodePreviewProps = {
  /** 文件正文（UTF-8）。内容变化时原地更新模型，不重建编辑器（保留滚动位置）。 */
  value: string;
  /** 文件路径：用于按扩展名推断语言（未知 → plaintext）。 */
  path?: string;
  /** 显式 Monaco 语言 id，优先于 path 推断。 */
  language?: string;
  /**
   * 宿主注入的 Monaco 命名空间（装载器留在宿主，见 ./monaco）。还没装载好时是
   * null / undefined：容器保持空白，由调用方的加载态兜底；单测注入 fake 命名空间。
   */
  monaco?: MonacoNS | null;
  /** Monaco 无障碍标签（读屏）。 */
  ariaLabel?: string;
  className?: string;
};

// 只读代码 / 文本渲染器（Monaco）。语言按扩展名推断，未知扩展名纯文本。
export function CodePreview({
  value,
  path,
  language,
  monaco,
  ariaLabel,
  className,
}: CodePreviewProps) {
  const containerRef = React.useRef<HTMLDivElement>(null);
  const editorRef = React.useRef<MonacoCodeEditor | null>(null);
  const ns = monaco ?? null;
  const lang = language ?? (path ? monacoLanguageForPath(path) : "plaintext");
  useMonacoThemeSync(ns);

  // 编辑器只建一次：ns / 语言 / 无障碍标签变化才重建；value 刷新走下方
  // [value] effect 原地 setValue，避免轮次结束重读时重建编辑器丢滚动位置。
  React.useEffect(() => {
    if (!ns || !containerRef.current) return;
    const editor = ns.editor.create(containerRef.current, {
      value,
      language: lang,
      readOnly: true,
      automaticLayout: true,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      fontSize: 13,
      tabSize: 2,
      ariaLabel,
      theme: resolveMonacoTheme(),
    });
    editorRef.current = editor;
    return () => {
      editor.dispose();
      editorRef.current = null;
    };
    // value 故意不进 deps：见上方注释，由 [value] effect 维护。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ns, lang, ariaLabel]);

  React.useEffect(() => {
    editorRef.current?.setValue(value);
  }, [value]);

  return <div ref={containerRef} className={className} />;
}
