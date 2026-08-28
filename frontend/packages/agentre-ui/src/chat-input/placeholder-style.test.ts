import { existsSync, readFileSync } from "node:fs";
import { resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 占位文字是 `AIChatInput` 的一部分，所以它的 CSS 跟组件走。
 *
 * TipTap 的 `Placeholder` 扩展只往空段落上加 `is-editor-empty` 类和
 * `data-placeholder` 属性——**可见的那行字是 CSS 用 `content: attr()` 画出来的**。
 * 这条规则此前只存在于桌面端的 globals.css 里，于是 agentre-server 接了同一个
 * 组件、拼好了按能力分段的提示文案，用户却一个字都看不到；jsdom 不跑 CSS，
 * 两端的测试全绿。
 *
 * 因此这里守两件事，缺一件就等于没修：
 *   1. 规则本身在那份 CSS 里；
 *   2. **组件真的 import 了它**——文件躺在 dist 里没人 import，和不存在一样，
 *      而且这种漏法不报错。
 *
 * 第三件是它必须是**纯 CSS**：这份文件由 JS 的副作用 import 拉进宿主打包器，
 * 不经过 Tailwind 的处理管线，写 `@apply` 不会展开（宿主要么原样输出无效声明、
 * 要么在 CSS 解析期报错），所以只能用 var() 引 token。
 */
function locate(relative: string): string {
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

/**
 * 剥掉 CSS 注释再断言「有没有某个声明」。
 *
 * 不剥的话注释里**提到**某样东西就会被当成用了它——这份文件的注释正好写着
 * 「不写 @apply」，裸匹配于是自己把自己判红。本仓踩过同一类坑（token 契约测试
 * 当年用裸 indexOf 找 `.dark`，撞上注释里引用的 `.dark`）。
 */
const declarationsOf = (relative: string) =>
  read(relative).replace(/\/\*[\s\S]*?\*\//g, "");

describe("AIChatInput 自带占位文字样式", () => {
  it("空编辑器的占位文字由 content: attr(data-placeholder) 画出来", () => {
    const css = read("src/chat-input/chat-input.css");

    expect(css).toMatch(
      /\.ProseMirror\s+p\.is-editor-empty:first-child::before/,
    );
    expect(css).toMatch(/content:\s*attr\(data-placeholder\)/);
  });

  it("占位文字不参与布局也不吃点击", () => {
    // float+height:0 让它不把光标顶下去；pointer-events:none 让点它等于点编辑器。
    const css = read("src/chat-input/chat-input.css");

    expect(css).toMatch(/height:\s*0/);
    expect(css).toMatch(/pointer-events:\s*none/);
  });

  it("颜色引 token，且整份是纯 CSS（不经 Tailwind 管线，@apply 不会展开）", () => {
    const declarations = declarationsOf("src/chat-input/chat-input.css");

    expect(declarations).toMatch(/color:\s*var\(--(color-)?muted-foreground\)/);
    expect(declarations).not.toMatch(/@apply/);
  });

  it("组件自己 import 了它 —— 这条断了，文件进得了 dist 也没人加载", () => {
    const source = read("src/chat-input/index.tsx");

    expect(source).toMatch(/import\s+["']\.\/chat-input\.css["']/);
  });
});
