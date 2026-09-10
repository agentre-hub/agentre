import * as React from "react";

import type { MonacoNS } from "@agentre-hub/agentre-ui";

import { loadMonaco } from "@/lib/file-preview/monaco-loader";

/**
 * 宿主侧的 Monaco 装载接缝：异步经 loadMonaco() 动态加载命名空间，交给共享包的
 * 内容视图（CodePreview / DiffPreview / MarkdownSourceView）经 `monaco` prop 用。
 *
 * 装载器留在宿主，因为 worker 环境用的是 Vite 的 `?worker`（见 monaco-loader 的
 * 注释）；包按设计只收注入。加载失败时静默返回 null，容器保持空白，由面板的错误态
 * 兜底。单测把 `@/lib/file-preview/monaco-loader` 换成 fake（见 file-preview-panel
 * 的用例）。
 *
 * `enabled` 是懒加载闸门：当前这个标签的内容根本不经 Monaco 渲染（工具 diff /
 * 图片）时传 false，那个几 MB 的 chunk 就一次都不拉。
 */
export function useMonaco(enabled = true): MonacoNS | null {
  const [ns, setNs] = React.useState<MonacoNS | null>(null);

  React.useEffect(() => {
    if (!enabled) return;
    let cancelled = false;
    loadMonaco()
      .then((m) => {
        if (!cancelled) setNs(m);
      })
      .catch(() => {
        /* 加载失败：保持 null，容器留空。 */
      });
    return () => {
      cancelled = true;
    };
  }, [enabled]);

  return ns;
}
