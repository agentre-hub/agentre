import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 宿主外壳那两份样式表的契约。
 *
 * 包里的 CSS 分两类（spec 2026-08-21-cross-host-ui-alignment 决策 1）：
 * **组件自带的**跟组件走、由组件自己 import（见 chat-input/placeholder-style.test.ts）；
 * **宿主外壳的**——滚动条、body 字体、可点元素的手型、toast 主题——不属于任何一个
 * 组件，做成显式入口由宿主 import。这一份守的是后者。
 *
 * 为什么读文件而不是 import CSS：vitest 没开 `css: true`，CSS import 在测试里被
 * stub 掉，拿不到内容；就算开了，happy-dom 也不跑 Tailwind 的编译。直接读源文件
 * 才测得到真实声明。同 styles/tokens.test.ts。
 *
 * 少一条规则不会让任何东西报错，只会让对应的那块「没样式」——这正是要拦的失败：
 * 占位文字整个不显示、toast 退回 sonner 自带的饱和色、滚动条变回系统粗条。
 */
function locate(relative: string): string {
  // 两个入口都要能跑：宿主 app 的 vitest cwd 是 frontend/，包自己的 pnpm test
  // cwd 是 packages/agentre-ui/。happy-dom 下 import.meta.url 不保证是 file:，
  // 所以探 cwd 而不是从模块 URL 推。同 styles/tokens.test.ts。
  const found = [
    resolve(process.cwd(), "packages/agentre-ui", relative),
    resolve(process.cwd(), relative),
  ].find((candidate) => existsSync(candidate));

  if (!found) {
    throw new Error(`${relative} not found from either workspace root`);
  }

  return found;
}

const read = (relative: string) => readFileSync(locate(relative), "utf8");

describe("base.css —— 宿主外壳的基线", () => {
  it("body 用 token 里的字体栈，而不是各宿主自己写一串", () => {
    // 写死 `ui-sans-serif, system-ui` 的宿主会在 Windows / Linux 上把中文与
    // emoji 落到另一条回退链上：tokens.css 的 --font-sans 钉了 PingFang SC /
    // Microsoft YaHei / Noto Sans SC 和三个 emoji 家族，绕开它就等于不要它们。
    const css = read("styles/base.css");

    expect(css).toMatch(/body\s*{[^}]*font-family:\s*var\(--font-sans\)/s);
  });

  it("* 的边框色回到 --border", () => {
    // Tailwind v4 起默认边框色是 currentColor（v3 是 gray-200）。
    // 少这一条，`border` 类画出来的是跟文字同色的边框。
    const css = read("styles/base.css");

    expect(css).toMatch(/border-color:\s*var\(--border\)/);
  });

  it("可点的东西是手型，禁用态不是", () => {
    // Tailwind v4 把 preflight 里那条 `button { cursor: pointer }` 删了。
    // 放 base 层而不是靠每个组件自带 cursor-pointer：后者要靠每个作者记得抄，
    // 漏一个就是一处只有鼠标才发现得了的静默回归。
    const css = read("styles/base.css");

    expect(css).toMatch(/button:not\(:disabled\)/);
    expect(css).toMatch(/\[role="button"\]:not\(\[aria-disabled="true"\]\)/);
    expect(css).toMatch(/cursor:\s*pointer/);
  });

  it("滚动条走 --sb-thumb 变量，且默认透明", () => {
    // WKWebView 对 ::-webkit-scrollbar 的类/属性选择器切换不重绘，只有 CSS
    // 自定义属性的**值**变化才触发 thumb 重绘。所以显隐必须走变量，
    // 由 useAutoHideScrollbars 改值——它俩少任何一半，滚动条都不会出现。
    const css = read("styles/base.css");

    expect(css).toMatch(/--sb-thumb:\s*transparent/);
    expect(css).toMatch(
      /::-webkit-scrollbar-thumb[^{]*{[^}]*var\(--sb-thumb\)/s,
    );
    expect(css).toMatch(/scrollbar-color:\s*var\(--sb-thumb\)/);
  });

  it("scrollbar-none 工具类在，且保留滚动能力", () => {
    // 横向条带（tab strip、面包屑）用它，避免滚动条占走固定高度容器的可视空间。
    // 只能隐藏外观，不能 overflow:hidden——那会把滚动本身也关掉。
    const css = read("styles/base.css");

    expect(css).toMatch(/\.scrollbar-none/);
    expect(css).toMatch(/scrollbar-width:\s*none/);
    expect(css).not.toMatch(/\.scrollbar-none[^}]*overflow:\s*hidden/s);
  });
});

describe("toast.css —— sonner 的 token 映射", () => {
  it("四档语义各自绑到 token，而不是 sonner 自带的饱和色", () => {
    // 宿主开 richColors 却不 import 这份，就是今天 agentre-server 的样子：
    // 同一条「保存成功」桌面端是淡色面、本站是一条绿条。
    const css = read("styles/toast.css");

    expect(css).toMatch(/--success-bg:\s*var\(--status-running-bg\)/);
    expect(css).toMatch(/--error-bg:\s*var\(--destructive-soft\)/);
    expect(css).toMatch(/--warning-bg:\s*var\(--status-waiting-bg\)/);
    expect(css).toMatch(/--info-bg:\s*var\(--primary-soft\)/);
  });

  it("正文走中性色，只有图标用饱和 chroma", () => {
    // soft tinted 底 + 中性文字是 Linear 那种克制的形态；
    // 文字也跟着饱和就退回 richColors 默认的告警条了。
    const css = read("styles/toast.css");

    expect(css).toMatch(/--success-text:\s*var\(--foreground\)/);
    expect(css).toMatch(/--error-text:\s*var\(--foreground\)/);
    expect(css).toMatch(
      /\[data-type="error"\][^{]*\[data-icon\][^{]*{[^}]*var\(--destructive\)/s,
    );
  });
});

describe("两份样式表都从包的 exports 出得去", () => {
  it("package.json 声明了 ./base.css 与 ./toast.css", () => {
    // 文件写好了但没进 exports，宿主 import 会在解析期炸——而且报的是宿主的错。
    const pkg = JSON.parse(read("package.json")) as {
      exports: Record<string, unknown>;
      files: string[];
    };

    expect(pkg.exports["./base.css"]).toBe("./styles/base.css");
    expect(pkg.exports["./toast.css"]).toBe("./styles/toast.css");
    // styles/ 已在 files 里，新文件才跟着进 tarball。
    expect(pkg.files).toContain("styles");
  });
});
