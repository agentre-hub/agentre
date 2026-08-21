import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

import {
  ARBITRARY_FONT_SIZE,
  restrictedSyntax,
} from "../../eslint-rules/design-tokens.js";

/**
 * 字号守卫的守卫。
 *
 * 两件事要钉住：
 *   1. **正则真的拦得住 / 放得过**。规则写错的表现不是报错，是它默默不生效——
 *      lint 全绿，而字面像素继续攒（这一轮扫掉的 109 处就是这么攒起来的）。
 *   2. **eslint.config.js 消费的是同一份来源**。规则数据单独成模块的全部理由就是
 *      这个：否则守卫测试可能在测一份和实际生效的配置不一样的正则。
 *
 * 判据是「这个尺寸有没有对应的 token」而不是「所有字面像素都禁」——8 / 9px 与
 * 展示字号目前没有档，放行是有意的（见 eslint-rules/design-tokens.js 的注释）。
 */
const FRONTEND_ROOT = path.resolve(__dirname, "../..");
const matcher = new RegExp(ARBITRARY_FONT_SIZE);

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
