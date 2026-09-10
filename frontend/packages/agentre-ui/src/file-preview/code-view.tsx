import * as React from "react";

import { clampAnchor, type PreviewRevealTarget } from "./anchor";
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
  /**
   * 要定位到的那一段（转录里点了一条带行号的链接）。缺席 = 不定位，滚动位置完全
   * 由用户掌握。`nonce` 变一次就重新定位一次，见 ./anchor。
   */
  revealTarget?: PreviewRevealTarget;
  className?: string;
};

// 只读代码 / 文本渲染器（Monaco）。语言按扩展名推断，未知扩展名纯文本。
export function CodePreview({
  value,
  path,
  language,
  monaco,
  ariaLabel,
  revealTarget,
  className,
}: CodePreviewProps) {
  const containerRef = React.useRef<HTMLDivElement>(null);
  const editorRef = React.useRef<MonacoCodeEditor | null>(null);
  // 已经为哪一个 nonce 定位过。轮次结束重读正文时 effect 会再跑一遍，靠它把
  // 「重新定位」限定在真的又点了一次链接的时候——否则每次重读都会把用户从他滚
  // 到的地方拽回去。编辑器重建时清空（新实例没有滚动位置可言）。
  const revealedNonceRef = React.useRef<number | null>(null);
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
    revealedNonceRef.current = null;
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

  // 定位排在 value effect 之后:模型里必须已经是这次要看的正文,否则行数还是上一
  // 版的、甚至是空的。空正文一律不定位——首次打开时正文尚未读回来就是这一态,在
  // 空模型上定位一次会把 nonce 消耗掉,真正的正文到达后就再也不滚了。
  React.useEffect(() => {
    const editor = editorRef.current;
    if (!editor || !revealTarget || value === "") return;
    if (revealedNonceRef.current === revealTarget.nonce) return;
    const model = editor.getModel();
    if (!model) return;
    revealedNonceRef.current = revealTarget.nonce;
    const { startLine, endLine } = clampAnchor(
      revealTarget,
      model.getLineCount(),
    );
    editor.revealLineInCenter(startLine);
    editor.setSelection({
      startLineNumber: startLine,
      startColumn: 1,
      endLineNumber: endLine,
      endColumn: model.getLineMaxColumn(endLine),
    });
    // ns / lang / ariaLabel 进 deps 是因为它们变一次编辑器就重建一次,新实例要
    // 重新定位——effect 自己不读它们,但没有它们就跑不到新编辑器上。
  }, [ns, lang, ariaLabel, value, revealTarget]);

  return <div ref={containerRef} className={className} />;
}
