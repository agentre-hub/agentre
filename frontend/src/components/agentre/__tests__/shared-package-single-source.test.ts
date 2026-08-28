import fs from "node:fs";
import os from "node:os";
import path from "node:path";

import { describe, expect, it } from "vitest";

/**
 * 共享包已发布模块的「桌面不留副本」守卫。
 *
 * 这一轮清掉的五份桌面副本（`openclaw-backend-fields` / `openclaw-validation` /
 * `agent-backends-utils` / `device-identity` / `app-dialog`）全是上一次「只搬不守」
 * 的产物：搬进包之后桌面那份没删，两边逐行同构地各自演化，`git log` 各看各的。
 * 既有单测拦不住这种回流——它们只看渲染结果，谁在渲染无所谓。所以这里扫源码，
 * 与 `composer-single-source.test.ts` 是同一种守卫。
 *
 * 判据：包经唯一公开出口（`packages/agentre-ui/src/index.ts`）发出去的模块名，
 * 桌面 `src/components/agentre/` 下不许有同名的**非转发**文件。转发是合法的：
 * 装配根（`agent-backends.tsx`）只是把宿主能力接到包组件上，它从包取实现，不是
 * 第二份实现。
 *
 * 基础组件已经没有桌面侧目录可扫了——`src/components/ui/` 那层转发整个删掉，调用点
 * 直接从包 import（见 docs/frontend.md）。
 *
 * 守卫读包的**源码树**而不是 `dist`：桌面经 workspace 直连源码，没有已安装的
 * `dist` 可读。
 *
 * 自证不空过：读包目录得空集时本文件必须红。只断言「留下的那份必须是转发」在包被
 * 读空（改名、路径写错、workspace 没装）时会生成零个用例，全绿既可能是「都转发了」
 * 也可能是「什么都没查」——`agentre-server` 那份守卫头部记录的正是这个洞。
 */

const PACKAGE = "@agentre-hub/agentre-ui";
const FRONTEND_ROOT = path.resolve(__dirname, "../../../..");
const PACKAGE_SRC = path.join(FRONTEND_ROOT, "packages/agentre-ui/src");
const SCANNED_DIRS = ["src/components/agentre"];

/**
 * 登记「桌面刻意保留自己那份」的理由。空着就是没有分叉——
 * 往里加一条要写清楚包里那份为什么不够用。
 */
const KEEP_LOCAL: Array<[string, string]> = [];

/** 把一个相对 specifier 解析成包源码树里的真实文件，解析不到给 null。 */
function resolveModule(specifier: string, fromFile: string): string | null {
  const base = path.resolve(path.dirname(fromFile), specifier);
  const candidates = [
    `${base}.ts`,
    `${base}.tsx`,
    path.join(base, "index.ts"),
    path.join(base, "index.tsx"),
  ];
  for (const candidate of candidates) {
    if (fs.existsSync(candidate) && fs.statSync(candidate).isFile()) {
      return candidate;
    }
  }
  return null;
}

/**
 * 收集自公开出口发得出去的模块名（basename，不含扩展名）。
 *
 * 走的是「从 index.ts 出发能到达」而不是「index.ts 那一行直接写了」：
 * `openclaw-backend-fields` 这类模块经面板间接发给消费方，桌面再写一份同样是分叉。
 * `index` 不算模块名——那是各目录的桶文件，不是某个概念的实现。
 */
function publishedModuleNames(entryDir: string): Set<string> {
  const entry = resolveModule("./index", path.join(entryDir, "entry.ts"));
  const names = new Set<string>();
  if (!entry) return names;

  const seen = new Set<string>();
  const queue = [entry];
  while (queue.length > 0) {
    const file = queue.pop() as string;
    if (seen.has(file)) continue;
    seen.add(file);
    const source = fs.readFileSync(file, "utf8");
    for (const match of source.matchAll(/from\s+"(\.[^"]*)"/g)) {
      const resolved = resolveModule(match[1], file);
      if (resolved) queue.push(resolved);
    }
    const name = path.basename(file).replace(/\.tsx?$/, "");
    if (name !== "index") names.add(name);
  }
  return names;
}

/** 从一份源码里收集所有自 `@agentre-hub/agentre-ui` 取来的名字（import 与 re-export 都算）。 */
function importedFromPackage(source: string): Set<string> {
  const names = new Set<string>();
  const pattern = new RegExp(
    `(?:import|export)\\s+(?:type\\s+)?\\{([^}]*)\\}\\s+from\\s+"${PACKAGE.replace("/", "\\/")}"`,
    "g",
  );
  for (const match of source.matchAll(pattern)) {
    for (const raw of match[1].split(",")) {
      const name = raw
        .trim()
        .replace(/^type\s+/, "")
        .split(/\s+as\s+/)[0];
      if (name) names.add(name);
    }
  }
  // `export * from "pkg"` 也是转发，只是它不写名字——记一个 `*`，免得整目录转发
  // 的写法被当成副本。
  if (
    new RegExp(
      `export\\s+\\*\\s+from\\s+"${PACKAGE.replace("/", "\\/")}"`,
    ).test(source)
  ) {
    names.add("*");
  }
  return names;
}

function hostModules(dir: string): string[] {
  return fs
    .readdirSync(path.join(FRONTEND_ROOT, dir))
    .filter((file) => /\.tsx?$/.test(file) && !/\.test\.tsx?$/.test(file))
    .map((file) => `${dir}/${file}`);
}

describe("共享包已发布的模块，桌面不留副本", () => {
  const published = publishedModuleNames(PACKAGE_SRC);

  it("Given 包源码树, When 守卫遍历公开出口, Then 收得到已发布模块名（读空则下面那条是假绿）", () => {
    // 空目录确实产出空集——所以上面那个 size 断言不是恒真的摆设，包被读空时它会红。
    const emptyDir = fs.mkdtempSync(
      path.join(os.tmpdir(), "agentre-ui-empty-"),
    );
    try {
      expect(publishedModuleNames(emptyDir).size).toBe(0);
    } finally {
      fs.rmSync(emptyDir, { recursive: true, force: true });
    }

    expect(
      published.size,
      `读不到 ${PACKAGE_SRC} 的已发布模块名：这条一红，本文件其余断言全部是假绿。`,
    ).toBeGreaterThan(0);
  });

  it("Given 包已发布的模块名, When 桌面存在同名文件, Then 那份文件从包取实现而不是自己写一份", () => {
    const copies = SCANNED_DIRS.flatMap(hostModules)
      .filter((file) =>
        published.has(path.basename(file).replace(/\.tsx?$/, "")),
      )
      .filter((file) => !KEEP_LOCAL.some(([kept]) => kept === file))
      .filter(
        (file) =>
          importedFromPackage(
            fs.readFileSync(path.join(FRONTEND_ROOT, file), "utf8"),
          ).size === 0,
      )
      .sort();

    expect(
      copies,
      `这些名字包里已经有了，桌面这几份是第二份实现：${copies.join("、")}。` +
        `删掉它们、调用点改从 "${PACKAGE}" import；` +
        `包里那份确实不够用，就去 KEEP_LOCAL 登记理由。`,
    ).toEqual([]);
  });
});
