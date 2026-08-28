import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 桌面端这一侧的接线证明：宿主外壳的样式**从包里来**，本仓不再自持一份。
 *
 * 这几样（body 字体、`border` 的默认色、可点元素的手型、滚动条、sonner 配色、
 * 输入框占位文字）此前逐条写在本仓的 globals.css 里，于是 agentre-server 接同一套
 * 组件时全部落空——占位文字整个不显示、toast 退回 sonner 自带的饱和条。抽进包
 * （spec 2026-08-21-cross-host-ui-alignment）之后，本仓的职责只剩「接上」。
 *
 * 这份守卫拦两种倒退：
 *   1. **忘了接**——包发出来了、宿主没 import，界面静默地少一层样式；
 *   2. **接了又自己再写一份**——两份同时存在时看不出问题，等包那边一改就分叉，
 *      而本仓的测试照样全绿（这正是当初三份副本的长法）。
 *
 * 断言前先剥注释：注释里**提到**某个选择器不等于用了它，裸匹配会把讲解文字判成
 * 真声明。本仓踩过同一类坑（token 契约测试当年用裸 indexOf 找 `.dark`，
 * 撞上注释里引用的 `.dark`）。
 */
const FRONTEND_ROOT = path.resolve(__dirname, "../..");

const readStripped = (relative: string) =>
  fs
    .readFileSync(path.join(FRONTEND_ROOT, relative), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "");

const globals = () => readStripped("src/styles/globals.css");

describe("桌面端接上了包的宿主样式基线", () => {
  it("globals.css import 了 base.css 与 toast.css", () => {
    const css = globals();

    expect(css).toMatch(/@import\s+"@agentre-hub\/agentre-ui\/base\.css"/);
    expect(css).toMatch(/@import\s+"@agentre-hub\/agentre-ui\/toast\.css"/);
  });

  it("vite 给这两个入口配了别名 —— 少了它桌面端解析不到，且不报错", () => {
    // 本仓把整个包别名到 packages/agentre-ui/src，所以每个子路径出口都要单独
    // 一条（tokens.css / code-highlight.css 已有先例）。漏配的表现不是构建失败，
    // 是那份 CSS 悄悄没进产物。
    const config = fs.readFileSync(
      path.join(FRONTEND_ROOT, "vite.config.ts"),
      "utf8",
    );

    expect(config).toContain("@agentre-hub/agentre-ui/base.css");
    expect(config).toContain("@agentre-hub/agentre-ui/toast.css");
  });

  it("App.tsx 用包里的滚动条 hook，不再自己定义一个", () => {
    const app = fs.readFileSync(
      path.join(FRONTEND_ROOT, "src/App.tsx"),
      "utf8",
    );

    expect(app).toMatch(/useAutoHideScrollbars\(\)/);
    expect(app).not.toMatch(/function\s+useAutoHideScrollbars/);
  });
});

describe("接了就不再自己写一份", () => {
  it.each([
    ["sonner 的 token 映射", /\[data-sonner-toaster\]/],
    ["滚动条变量", /--sb-thumb\s*:/],
    ["滚动条外观", /::-webkit-scrollbar/],
    ["scrollbar-none 工具类", /\.scrollbar-none/],
    ["输入框占位文字", /is-editor-empty/],
  ])("globals.css 里不再有 %s", (_label, pattern) => {
    expect(globals()).not.toMatch(pattern);
  });
});

describe("桌面端独有的那几样留在本仓", () => {
  it.each([
    ["Wails 拖拽区", /--wails-draggable/],
    ["全局禁选", /user-select:\s*none/],
    ["可选文本的豁免", /data-selectable-text/],
    ["窗口不整页滚动", /overflow:\s*hidden/],
  ])("globals.css 仍然有 %s", (_label, pattern) => {
    // 浏览器端没有这些需求（页面本来就该能滚、也不该禁选），所以它们**不**进包。
    expect(globals()).toMatch(pattern);
  });
});
