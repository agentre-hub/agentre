// ── Monaco 加载接缝（mock seam）──────────────────────────────────────────────
// 全仓库唯一触碰真实 Monaco 的模块。真实 monaco 走动态 import（懒加载独立 chunk，
// 不进初始包）+ 动态 import 的 worker 环境模块，离线桌面可跑、happy-dom 单测绝不执行。
//
// 内容视图（CodePreview / DiffPreview / MarkdownSourceView）住在共享包里，命名空间
// 由这里装载后经 `monaco` prop 注进去（use-monaco.ts 是那一层 React 接缝）。
//
// 组件测试 / task 6 测试的两种接缝（任选其一，推荐 1）：
//   1. 给组件传 monaco prop（fake 命名空间，见共享包 file-preview 组件签名）；
//   2. vi.mock("@/lib/file-preview/monaco-loader", () => ({ loadMonaco: vi.fn() }))
//      ，把 loadMonaco 替换成 resolve fake 的 stub —— 面板走的就是这条路径。
//
// spike 结论（task 5）：裸 monaco-editor + Vite `?worker`，不用 @monaco-editor/react。
// 离线 + //go:embed 禁止 CDN，@monaco-editor/react 的 @monaco-editor/loader 默认
// jsdelivr 拉取，即便 loader.config({ monaco }) 也是 CDN-first 设计；裸 monaco 无
// loader 概念（零 CDN 面）、只多一个依赖、worker 由我们按需配。diff editor 与普通
// editor 共用同一份动态 import 的 monaco 命名空间单例 → 复用同一 Monaco 实例。
//
// 体积控制：只读预览不需要 IntelliSense，故只动态加载 editor.api（核心编辑器）+
// 全部 basic-languages 词法高亮（纯 tokenizer，无 worker）；绝不引入完整包
// monaco-editor（editor.main 连带 ts/css/html/json 语言服务，ts.worker 单文件
// ~7MB）或 esm/vs/language/* 语言服务。

import type { MonacoNS } from "@agentre-hub/agentre-ui";

// 命名空间的类型由共享包持有（那边的内容视图按它收 prop），这里只转发 —— 两份
// 同名类型各自演化正是抽包要避免的那种分叉。
export type { MonacoNS };

let cached: Promise<MonacoNS> | null = null;

/** 动态加载 Monaco 命名空间（幂等，进程内单例）。 */
export function loadMonaco(): Promise<MonacoNS> {
  cached ??= (async () => {
    await import("./monaco-worker-env");
    await import("monaco-editor/basic-languages/monaco.contribution");
    const monaco = await import("monaco-editor/editor/editor.api");
    // json 不在 basic-languages 里（0.56 起移出），补一个纯词法的 JSON 语言。
    const { registerJsonLanguage } = await import("./monaco-json");
    registerJsonLanguage(monaco);
    return monaco;
  })();
  return cached;
}
