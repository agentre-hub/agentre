import * as React from "react";

import { monacoLanguageForPath } from "./monaco-language";
import {
  resolveMonacoTheme,
  useMonacoThemeSync,
  type MonacoNS,
} from "./monaco";

export type DiffPreviewProps = {
  /** 左列 = HEAD 版本（未跟踪文件传空串 → 全部新增）。 */
  original: string;
  /** 右列 = 工作区版本。 */
  modified: string;
  /** 文件路径：用于按扩展名推断语言（未知 → plaintext）。 */
  path?: string;
  /** 显式 Monaco 语言 id，优先于 path 推断。 */
  language?: string;
  /** 宿主注入的 Monaco 命名空间（同 CodePreview，见 ./monaco）。 */
  monaco?: MonacoNS | null;
  /** Monaco 无障碍标签（读屏）。 */
  ariaLabel?: string;
  className?: string;
};

// 只读 diff 渲染器（Monaco diff editor）：左 = HEAD 版本、右 = 工作区。
// 与 CodePreview 共用宿主注入的同一份 Monaco 命名空间单例（见 ./monaco）。
// 内容（original/modified）变化时重建 diff editor —— 预览刷新频率低（轮次结束 /
// 切文件），重建可接受，且 setModel 模型所有权交由 diff editor 在 dispose 时释放。
export function DiffPreview({
  original,
  modified,
  path,
  language,
  monaco,
  ariaLabel,
  className,
}: DiffPreviewProps) {
  const containerRef = React.useRef<HTMLDivElement>(null);
  const ns = monaco ?? null;
  const lang = language ?? (path ? monacoLanguageForPath(path) : "plaintext");
  useMonacoThemeSync(ns);

  React.useEffect(() => {
    if (!ns || !containerRef.current) return;
    const diff = ns.editor.createDiffEditor(containerRef.current, {
      readOnly: true,
      automaticLayout: true,
      minimap: { enabled: false },
      scrollBeyondLastLine: false,
      fontSize: 13,
      renderSideBySide: true,
      ariaLabel,
      theme: resolveMonacoTheme(),
    });
    const originalModel = ns.editor.createModel(original, lang);
    const modifiedModel = ns.editor.createModel(modified, lang);
    diff.setModel({ original: originalModel, modified: modifiedModel });
    return () => {
      diff.dispose();
      // Monaco 里 createModel 建的模型登记在全局模型表,编辑器 dispose 不会释放
      // 它们;不显式 dispose 每次重建就泄漏 2 个 TextModel(轮次结束重读 / 切文件
      // 都会重建)。先 diff.dispose() 解除编辑器引用,再释放模型。
      originalModel.dispose();
      modifiedModel.dispose();
    };
  }, [ns, lang, original, modified, ariaLabel]);

  return <div ref={containerRef} className={className} />;
}
