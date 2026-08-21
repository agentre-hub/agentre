import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 住在共享包里的那些基础组件，本仓只留一层转发，不留实现。
 *
 * 保留 `@/components/ui/*` 这个路径是有意的：它是本仓既有的 shadcn 约定，几十个
 * 调用点从这里取符号，一次性改写会把搬迁的真实 diff 埋掉。换掉的只是**背后那份
 * 实现从哪来**。
 *
 * 这份守卫拦的是「副本重新长回来」：本地这份和包里那份此刻一模一样时什么都不会
 * 红，等包那边加一档 size 或换一个语义 token，桌面端跟着走、另一个宿主留在原地，
 * 而两边的 git log 各看各的。dialog / dropdown-menu / alert 就是这么变成三份的
 * （连包自己都私藏了一份）。
 *
 * 判据是「除注释外只剩转发语句」，不是「文件很短」——短的副本也是副本。
 */
const UI_DIR = path.resolve(__dirname, "..");

/** 实现已在包里、本仓只该转发的那些。 */
const FORWARDED = [
  "alert",
  "badge",
  "button",
  "dialog",
  "dropdown-menu",
  "hover-card",
  "input",
  "spinner",
  "textarea",
  "tooltip",
];

/** 去掉块注释、行注释与空行之后剩下的语句。 */
function statementsOf(file: string): string[] {
  return fs
    .readFileSync(path.join(UI_DIR, `${file}.tsx`), "utf8")
    .replace(/\/\*[\s\S]*?\*\//g, "")
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line.length > 0 && !line.startsWith("//"));
}

describe("共享基础组件在本仓只有一层转发", () => {
  it.each(FORWARDED)("%s.tsx 没有本地实现", (file) => {
    const body = statementsOf(file).join(" ");

    // 有这些就是在本地重新实现了一遍，而不是转发。
    expect(body).not.toMatch(/\bcva\(/);
    expect(body).not.toMatch(/\bfunction\b/);
    expect(body).not.toMatch(/className=/);
  });

  it.each(FORWARDED)("%s.tsx 的符号来自共享包", (file) => {
    // 折叠成一行再判：转发块常常是多行的（`export {` / 符号 / `} from "…"`），
    // 按行要求「每行都以 export 开头」会把那些行判成违规。
    const body = statementsOf(file).join(" ");

    expect(body).toContain("@agentre-ai/agentre-ui");
    // 除了转发，不该再有别的语句 —— 尤其不该有 import 进来自己再包一层。
    expect(body.startsWith("export")).toBe(true);
  });
});
