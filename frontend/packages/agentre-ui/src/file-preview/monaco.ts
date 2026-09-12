import * as React from "react";

// ── 共享包这一侧的 Monaco 契约 ─────────────────────────────────────────────
//
// **只有类型**：命名空间本身由宿主经 `monaco` prop 注入，包内一行 monaco 代码都不
// 加载。装载器与 worker 环境留在宿主，因为 worker 靠 Vite 的 `?worker` 后缀打成
// 独立 chunk，而本包是纯 tsc 构建（桌面端见 src/lib/file-preview/monaco-loader.ts）。
//
// 为什么用 monaco 自己的类型而不是手写一份「只含本包用到的那几个方法」的窄接口：
// 试过，diff editor 那一处过不去 —— 真实 `setModel` 收的是
// `IDiffEditorModel | IDiffEditorViewModel | null`，而窄接口的
// `{ original; modified }` 与 view model 两个方向都不可赋值，结果只能把参数放成
// `any` 才编得过。用真实类型换来的代价是一条 manifest 依赖：**monaco-editor 是
// peer**，与「渲染必须落在宿主装载的那一个实例上（worker 环境按实例配一次）」是
// 同一条判据（见 boundary.test.ts 头部的 peer/dep 分野）；包对它零运行时依赖，
// devDependencies 里那份只服务本包自己的 tsc / vitest。
//
// 体积面由宿主的装载器把关：只读预览只加载 editor.api + basic-languages 词法，
// 不引语言服务。editor.api 的类型面（editor / languages / Uri 等）已经够用；
// monaco-editor 0.56 的 exports 映射自带 esm/vs/ 前缀，深导入子路径不带前缀。
export type MonacoNS = typeof import("monaco-editor/editor/editor.api");

export type MonacoCodeEditor = ReturnType<MonacoNS["editor"]["create"]>;

// Monaco 主题跟随应用 .dark class（与 xterm 的 terminal-theme 同一判定）。
export function resolveMonacoTheme(): "vs-dark" | "vs" {
  return typeof document !== "undefined" &&
    document.documentElement.classList.contains("dark")
    ? "vs-dark"
    : "vs";
}

// 让已创建的 Monaco 编辑器跟随应用明暗切换（terminal-panel 同款 MutationObserver
// 先例）：编辑器建好后主题是全局的，app 在 documentElement 上翻转 .dark 时调
// ns.editor.setTheme 重涂；不重建编辑器，保留滚动位置。单测 fake monaco 需带
// editor.setTheme（见各组件测试的 fake 定义）。
export function useMonacoThemeSync(ns: MonacoNS | null): void {
  React.useEffect(() => {
    if (!ns || typeof document === "undefined") return;
    const apply = () => ns.editor.setTheme(resolveMonacoTheme());
    // 挂载时先按当前主题涂一次（与 terminal 同一理由：App 的 layout effect 可能
    // 晚于本组件首次 mount，observer 看不到那次 class 翻转）。
    apply();
    const observer = new MutationObserver(apply);
    observer.observe(document.documentElement, {
      attributes: true,
      attributeFilter: ["class"],
    });
    return () => observer.disconnect();
  }, [ns]);
}
