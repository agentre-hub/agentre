import { existsSync, readFileSync, readdirSync } from "node:fs";
import { dirname, join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 「只剩一份」的机械证明。
 *
 * 合并做完之后最容易倒退的不是代码写错，而是**旧的那份还躺在原地**：两份同时存在
 * 时一切正常，等有人改了其中一份才分叉，而那时谁都不知道该改哪份。所以这里不测
 * 行为，只测「另外两份确实没了、引用确实改指了」。
 *
 * 引擎面板（LlmProvidersPanel / AgentBackendsPanel）此前吃的是 engine/ui/ 下的
 * 私有副本，那份带着调色板字面色的遮罩——于是 agentre-server 的设置页弹窗和它
 * 自己其它弹窗压暗的颜色不一样，而宿主的 lint 管不到 node_modules。
 *
 * 判「引没引私有副本」必须**把相对路径解析成绝对路径再比**，不能看路径长相：
 * 合并前 engine/llm-provider-models/ 里写的是 `../ui/dialog`，合并后 engine/ 顶层
 * 写的也是 `../ui/dialog`——同样的字面量，一个指私有副本、一个指共用那份。
 */
const packageRoot = [
  resolve(process.cwd(), "packages/agentre-ui"),
  resolve(process.cwd()),
].find((candidate) => existsSync(join(candidate, "src/ui")));

if (!packageRoot) {
  throw new Error("package root not found from either workspace root");
}

const MERGED = ["dialog", "dropdown-menu", "alert"] as const;

/** 私有副本当初所在的绝对路径（无扩展名）。 */
const RETIRED = MERGED.map((name) => join(packageRoot, "src/engine/ui", name));

/** engine/ 下所有 .ts / .tsx 源文件。 */
function engineSources(dir: string): string[] {
  const out: string[] = [];
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const full = join(dir, entry.name);
    if (entry.isDirectory()) {
      out.push(...engineSources(full));
      continue;
    }
    if (/\.tsx?$/.test(entry.name)) out.push(full);
  }
  return out;
}

// 刻意不写成带 from 的正则：boundary.test.ts 用文本扫描找模块说明符，那种形状
// 会让它把这里的正则本身当成一处 import 报出来（见 hooks 那份守卫里的同一条）。
const UI_SPECIFIER = /["']([^"']*\/ui\/[^"']*)["']/;

describe("三份副本只剩一份", () => {
  it.each(MERGED)("engine/ui/%s.tsx 已经不存在", (name) => {
    expect(existsSync(join(packageRoot, `src/engine/ui/${name}.tsx`))).toBe(
      false,
    );
  });

  it("engine/ 下没有任何一处还引着那三个私有副本", () => {
    const stale = engineSources(join(packageRoot, "src/engine"))
      .flatMap((file) =>
        readFileSync(file, "utf8")
          .split("\n")
          .flatMap((line) => {
            const match = UI_SPECIFIER.exec(line);
            if (!match || !match[1].startsWith(".")) return [];
            const target = resolve(dirname(file), match[1]);
            return RETIRED.includes(target)
              ? [`${relative(packageRoot, file)} -> ${match[1]}`]
              : [];
          }),
      )
      .sort();

    expect(stale).toEqual([]);
  });

  it.each(MERGED)("合并后那份从包的公开出口出得去（%s）", (name) => {
    // 宿主拿不到就只能继续留自己那份 —— 副本就是这么回来的。
    // 折叠空白再找：三份里有两份是多行 export 块，按行匹配会漏判。
    const barrel = readFileSync(
      join(packageRoot, "src/index.ts"),
      "utf8",
    ).replace(/\s+/g, " ");

    expect(barrel).toContain(`} from "./ui/${name}";`);
  });
});
