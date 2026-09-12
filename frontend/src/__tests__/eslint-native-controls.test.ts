import path from "node:path";

import { ESLint } from "eslint";
import { beforeAll, describe, expect, it } from "vitest";

/**
 * 原生表单控件守卫的守卫。
 *
 * 「表单控件统一走 shadcn / 共享包」这条在 AGENTS.md 里写了很久，但**一直没有
 * 机械检查**——于是共享包里没有 Select / Checkbox 的那段时间，父项目下拉与
 * 「显示隐藏项」就地退回了系统控件，深色下配色与 tokens.css 无关，谁都没发现。
 *
 * 判据交给 eslint 而不是另写一个扫源码的用例：编辑器里当场报，且和设计 token
 * 那条守卫共用同一条 `no-restricted-syntax`——同一件事只留一套机制。
 *
 * 加载的是**项目真实的** eslint.config.js：就地拼一份配置的守卫，可以在规则根本
 * 没挂进配置时通过。
 */
const FRONTEND_ROOT = path.resolve(__dirname, "../..");

let eslint: ESLint;

// 预热单独摆在 beforeAll：第一次 lintText 才会解析整份配置（连同
// typescript-eslint 与插件），冷跑好几秒，记到第一条用例头上会让它随并发时红时绿。
beforeAll(async () => {
  eslint = new ESLint({ cwd: FRONTEND_ROOT, errorOnUnmatchedPattern: false });
  await eslint.lintText("export const warmup = 1;\n", {
    filePath: path.join(FRONTEND_ROOT, "src/warmup.ts"),
  });
}, 60_000);

async function lintAs(filePath: string, code: string) {
  const results = await eslint.lintText(code, {
    filePath: path.join(FRONTEND_ROOT, filePath),
  });
  return (results[0]?.messages ?? []).map((m) => m.ruleId);
}

describe("原生表单控件守卫", () => {
  it.each([
    ["原生下拉", `export const a = () => <select><option /></select>;`],
    ["原生多行输入", `export const a = () => <textarea value="" />;`],
    ["原生复选框", `export const a = () => <input type="checkbox" />;`],
    ["原生单选", `export const a = () => <input type="radio" />;`],
    ["原生搜索框", `export const a = () => <input type="search" value="" />;`],
  ])("%s 在宿主代码里报错", async (_label, code) => {
    expect(await lintAs("src/components/agentre/sample.tsx", code)).toContain(
      "no-restricted-syntax",
    );
  });

  it("共享包的领域代码同样报错 —— 退让最初就发生在包里", async () => {
    expect(
      await lintAs(
        "packages/agentre-ui/src/project/sample.tsx",
        `export const a = () => <select />;`,
      ),
    ).toContain("no-restricted-syntax");
  });

  it("原语自己的实现放行 —— 原生标签总得有一处住处", async () => {
    expect(
      await lintAs(
        "packages/agentre-ui/src/ui/sample.tsx",
        `export const a = () => <textarea />;`,
      ),
    ).not.toContain("no-restricted-syntax");
  });

  it("放行的豁免只针对原生控件，设计 token 那条在原语里照旧报", async () => {
    expect(
      await lintAs(
        "packages/agentre-ui/src/ui/sample.tsx",
        `export const a = "bg-slate-900";`,
      ),
    ).toContain("no-restricted-syntax");
  });

  it("文件选择器放行 —— 它没有可替代的原语形态，总是藏起来由按钮触发", async () => {
    expect(
      await lintAs(
        "src/components/agentre/sample.tsx",
        `export const a = () => <input type="file" className="hidden" />;`,
      ),
    ).not.toContain("no-restricted-syntax");
  });
});
