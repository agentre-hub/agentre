import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import {
  ARBITRARY_FONT_SIZE,
  LITERAL_COLOR_CLASS,
  RAW_COLOR_VALUE,
  restrictedSyntax,
} from "../../eslint-rules/design-tokens.js";

/**
 * 设计 token 守卫的守卫（颜色 + 字号两组）。
 *
 * 两件事要钉住：
 *   1. **正则真的拦得住 / 放得过**。规则写错的表现不是报错，是它默默不生效——
 *      lint 全绿，而字面像素继续攒（这一轮扫掉的 109 处就是这么攒起来的）。
 *   2. **eslint.config.js 消费的是同一份来源**。规则数据单独成模块的全部理由就是
 *      这个：否则守卫测试可能在测一份和实际生效的配置不一样的正则。
 *
 * 字号那组的判据是「这个尺寸有没有对应的 token」而不是「所有字面像素都禁」——
 * 8 / 9px 与展示字号目前没有档，放行是有意的（见 eslint-rules/design-tokens.js）。
 * 颜色那组没有这种「暂时没档」的放行：调色板色名一律禁，确实拿不到 token 的地方
 * （xterm 的 theme API）在 eslint.config.js 里按文件豁免并写明理由。
 */
const FRONTEND_ROOT = path.resolve(__dirname, "../..");
const matcher = new RegExp(ARBITRARY_FONT_SIZE);
const colorClass = new RegExp(LITERAL_COLOR_CLASS);
const rawColor = new RegExp(RAW_COLOR_VALUE);

describe("字号阶梯的 lint 规则", () => {
  it.each([
    ['className="text-[10px]"', "10px 有 text-3xs"],
    ['className="text-[11px]"', "11px 有 text-2xs"],
    ['className="text-[12px]"', "12px 有 text-xs"],
    ['className="text-[13px]"', "13px 有 text-aux"],
    ['className="text-[14px]"', "14px 有 text-sm"],
    ['className="text-[15px]"', "15px 有 text-prose"],
    ['className="flex items-center text-[13px] font-medium"', "夹在长串里"],
    ['className="sm:text-[15px]"', "带断点变体"],
    ['className="group-hover:text-[11px]"', "带组合变体"],
  ])("拦住 %s（%s）", (sample) => {
    expect(matcher.test(sample)).toBe(true);
  });

  it.each([
    ['className="text-[8px]"', "徽标字形，没有对应档"],
    ['className="text-[9px]"', "设备指纹，没有对应档"],
    ['className="text-[22px]"', "展示字号，没有对应档"],
    ['className="text-2xs"', "已经是阶梯上的名字"],
    ['className="text-aux"', "已经是阶梯上的名字"],
    ['className="max-w-[520px]"', "不是字号"],
  ])("放过 %s（%s）", (sample) => {
    expect(matcher.test(sample)).toBe(false);
  });

  it("规则形态覆盖普通字符串与模板字符串两种写法", () => {
    // 只拦 Literal 的话，`` cn(`text-[13px] ${x}`) `` 会绕过去。
    const selectors = restrictedSyntax.map((rule) => rule.selector);

    expect(selectors.some((s) => s.startsWith("Literal["))).toBe(true);
    expect(selectors.some((s) => s.startsWith("TemplateElement["))).toBe(true);
  });

  it("eslint.config.js 用的就是这一份规则数据", () => {
    const config = fs.readFileSync(
      path.join(FRONTEND_ROOT, "eslint.config.js"),
      "utf8",
    );

    expect(config).toContain("./eslint-rules/design-tokens.js");
    expect(config).toContain("...restrictedSyntax");
  });
});

describe("调色板字面色类的 lint 规则", () => {
  it.each([
    ['className="bg-slate-900"', "带色阶"],
    ['className="text-white"', "不带色阶"],
    ['className="dark:bg-black/70"', "变体 + 不透明度"],
    ['className="text-red-500/50"', "色阶 + 不透明度同时出现"],
    ['className="flex items-center bg-zinc-100 p-2"', "夹在长串里"],
    ['className="bg-neutral-600"', "身份色板缺中性档时的写法"],
  ])("拦住 %s（%s）", (sample) => {
    expect(colorClass.test(sample)).toBe(true);
  });

  it.each([
    ['className="bg-background"', "语义 token"],
    ['className="text-foreground"', "语义 token"],
    ['className="bg-scrim"', "语义 token"],
    ['className="text-agent-foreground"', "身份色上的前景"],
    ['className="bg-agent-15"', "身份色板里的一档"],
    ['className="text-status-waiting"', "语义 token"],
  ])("放过 %s（%s）", (sample) => {
    expect(colorClass.test(sample)).toBe(false);
  });
});

describe("写死颜色值的 lint 规则", () => {
  it.each([
    ['const c = "#0f172a";', "六位十六进制"],
    ['const c = "rgba(0, 0, 0, .5)";', "rgba()"],
    ['const c = "hsl(210 40% 98%)";', "hsl()"],
  ])("拦住 %s（%s）", (sample) => {
    expect(rawColor.test(sample)).toBe(true);
  });

  it.each([
    ['const c = "var(--background)";', "引 token"],
    [
      'const c = "color-mix(in oklab, var(--primary) 28%, transparent)";',
      "由 token 混出",
    ],
  ])("放过 %s（%s）", (sample) => {
    expect(rawColor.test(sample)).toBe(false);
  });
});

describe("三组规则都有两种形态", () => {
  it("字号与颜色各自都覆盖 Literal 与 TemplateElement", () => {
    // 只拦 Literal 的话，模板字符串里的写法会绕过去。
    const shapes = (source: string) =>
      restrictedSyntax
        .filter((rule) => rule.selector.includes(source))
        .map((rule) => rule.selector.split("[")[0])
        .sort();

    for (const source of [
      ARBITRARY_FONT_SIZE,
      LITERAL_COLOR_CLASS,
      RAW_COLOR_VALUE,
    ]) {
      expect(shapes(source)).toEqual(["Literal", "TemplateElement"]);
    }
  });
});
