import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import { describe, expect, it } from "vitest";

/**
 * 抽包的**命门守卫**。
 *
 * 这个包要被 agentre-server 通过 git 依赖消费，那侧没有 Vite 的 `@` 别名、
 * 没有 `wailsjs/` 生成物、也没有桌面端的 zustand store。所以一旦有人把宿主
 * 耦合写进包里，编译期在桌面端**照样是绿的**（别名还在），要等 server 那边
 * 构建才炸——这条守卫把那个延迟暴露的失败拉回到本仓库的单测里。
 *
 * 扫源码文本而不是靠 tsc：tsc 只会告诉你「解析不到」，而桌面端解析得到。
 */

const HOST_COUPLING_RULES: { label: string; matches(spec: string): boolean }[] =
  [
    {
      // 先于通用的 `@/` 规则判定，报错信息才能指出真正的问题：
      // 全局状态属于宿主，包内组件只能靠 props / ports 拿到它。
      label: "宿主 zustand store（状态必须由宿主经 props/ports 注入）",
      matches: (spec) => spec === "@/stores" || spec.startsWith("@/stores/"),
    },
    {
      label: "宿主路径别名 @/（包在消费方没有这个别名）",
      matches: (spec) => spec === "@" || spec.startsWith("@/"),
    },
    {
      label: "Wails 绑定（桌面端独有，浏览器端不存在）",
      matches: (spec) => spec.includes("wailsjs"),
    },
  ];

/**
 * 两个入口都要能跑：宿主 vitest 的 cwd 是 `frontend/`，包自己的 `pnpm test`
 * 的 cwd 是 `frontend/packages/agentre-ui/`。用 package.json 的 name 判别，
 * 而不是赌某个目录存在。
 */
function locatePackageRoot(): string {
  const candidates = [
    resolve(process.cwd(), "packages/agentre-ui"),
    resolve(process.cwd()),
  ];

  for (const candidate of candidates) {
    const manifestPath = join(candidate, "package.json");
    if (!existsSync(manifestPath)) continue;

    const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as {
      name?: string;
    };
    if (manifest.name === "@agentre-hub/agentre-ui") return candidate;
  }

  throw new Error("@agentre-hub/agentre-ui package root not found");
}

const packageRoot = locatePackageRoot();
const sourceRoot = join(packageRoot, "src");

function walkSourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const fullPath = join(dir, entry.name);
    if (entry.isDirectory()) {
      walkSourceFiles(fullPath, out);
      continue;
    }
    if (/\.(ts|tsx)$/.test(entry.name)) out.push(fullPath);
  }
  return out;
}

type ImportSite = { file: string; line: number; specifier: string };

const SPECIFIER_PATTERNS = [
  /\bfrom\s+["']([^"']+)["']/,
  /^\s*import\s+["']([^"']+)["']/,
  /\bimport\(\s*["']([^"']+)["']/,
  /\brequire\(\s*["']([^"']+)["']/,
];

/**
 * 只认**模块说明符位置**的字符串——所以本文件自己把 `"@/"` 之类写成规则字面量
 * 不会自伤，守卫也就不需要给自己开豁免（开了豁免的守卫等于没有守卫）。
 */
function collectImportSites(): ImportSite[] {
  const sites: ImportSite[] = [];

  for (const file of walkSourceFiles(sourceRoot)) {
    const lines = readFileSync(file, "utf8").split("\n");
    lines.forEach((line, index) => {
      for (const pattern of SPECIFIER_PATTERNS) {
        const match = pattern.exec(line);
        if (!match) continue;
        sites.push({
          file: relative(packageRoot, file),
          line: index + 1,
          specifier: match[1],
        });
        break;
      }
    });
  }

  return sites;
}

function packageNameOf(specifier: string): string {
  const segments = specifier.split("/");
  return specifier.startsWith("@")
    ? segments.slice(0, 2).join("/")
    : segments[0];
}

describe("agentre-ui package boundary", () => {
  /**
   * 这个包被 agentre-server 通过 git 依赖消费,pnpm 只抽 `frontend/packages/agentre-ui`
   * **一个子目录** —— tarball 里没有兄弟包。所以对 `@agentre-hub/agentre-wire` 的任何
   * 一种声明都是死路:它没发布到 npm(装不上),`file:../agentre-wire` 在那侧根本不存在,
   * 而 packageExtensions / overrides / patch 全发生在抓取之后,消费方没有补救手段。
   *
   * 词表因此由 Go 生成器在本包里另写一份 `src/event-kinds.gen.ts`(与 agentre-wire 那份
   * 同源、由 TestGeneratedTSFresh 逐字节守住)。这条守卫钉的是结果:包里不许再有那个
   * 依赖 —— 桌面端是 workspace,重新加回去在本仓照样绿,要等 server 装依赖才炸。
   */
  it("Given agentre-ui is consumed from a Git subdirectory, When its manifest is read, Then no dependency field references the sibling wire package", () => {
    const manifest = JSON.parse(
      readFileSync(join(packageRoot, "package.json"), "utf8"),
    ) as Record<string, Record<string, string> | undefined>;

    const declaredIn = [
      "dependencies",
      "peerDependencies",
      "devDependencies",
      "optionalDependencies",
    ].filter((field) => manifest[field]?.["@agentre-hub/agentre-wire"]);

    expect(declaredIn).toEqual([]);
  });

  // 这一条来自 65e8db67(「ship immutable build artifacts」),守的是另一件事:
  // 消费方拿到的包里有构建产物。它与上面那条依赖守卫无关,单独一条才不会互相
  // 遮住信号 —— 本轮把它从上面那条里拆出来时一字未改。
  it("Given agentre-ui is consumed from a Git subdirectory, When the package is inspected, Then the built entry point is present", () => {
    expect(existsSync(join(packageRoot, "dist/index.js"))).toBe(true);
  });

  it("Given the engine settings source, When shared-package imports are scanned, Then it is present and has no host-only dependency", () => {
    const engineSources = walkSourceFiles(join(sourceRoot, "engine"));

    expect(engineSources.length).toBeGreaterThan(0);
    const violations = collectImportSites()
      .filter(({ file }) => file.startsWith("src/engine/"))
      .flatMap(({ file, line, specifier }) => {
        const rule = HOST_COUPLING_RULES.find((candidate) =>
          candidate.matches(specifier),
        );
        return rule ? [`${file}:${line} "${specifier}" —— ${rule.label}`] : [];
      });
    expect(violations).toEqual([]);
  });

  it("Given the shared package sources, When imports are scanned, Then no host-only module is referenced", () => {
    const violations = collectImportSites().flatMap(
      ({ file, line, specifier }) => {
        const rule = HOST_COUPLING_RULES.find((candidate) =>
          candidate.matches(specifier),
        );
        return rule ? [`${file}:${line} "${specifier}" —— ${rule.label}`] : [];
      },
    );

    expect(violations).toEqual([]);
  });

  /**
   * peer 还是 dep，判据只有一条：**正确性是否依赖「和宿主是同一个实例」**。
   *
   *   - peer —— 跨实例会静默坏掉的：react/react-dom（hooks dispatcher）、
   *     i18next + react-i18next（宿主持有的实例与 I18nextProvider context）、
   *     radix-ui（`TooltipProvider` 挂在宿主的 chat.tsx，第二份 radix 的
   *     `Tooltip.Root` 找不到它的 context）、sonner（`toast()` 写模块级
   *     ToastState，`<Toaster/>` 挂在宿主 App.tsx，第二份的 toast 谁也看不见）。
   *   - dep —— 纯渲染/纯函数，多一份只是体积：react-markdown / remark-gfm /
   *     rehype-highlight / lucide-react / @iconify/react（离线 IconifyIcon 对象，
   *     不碰共享 registry）/ clsx / tailwind-merge / cva / @xterm/*。
   *
   * 刻意**不声明**的两个：zustand（全局状态属于宿主，包只收 props/ports）、
   * react-router-dom（跳转是外壳能力，该走 ports 而不是包内 `useNavigate`）。
   * 两者都会被这条守卫拦下——桌面端 hoisting 让它们「碰巧能解析」。
   */
  it("Given the shared package sources, When bare imports are scanned, Then every npm package is declared in package.json", () => {
    const manifest = JSON.parse(
      readFileSync(join(packageRoot, "package.json"), "utf8"),
    ) as Record<string, Record<string, string> | undefined>;
    const declared = new Set([
      ...Object.keys(manifest.dependencies ?? {}),
      ...Object.keys(manifest.peerDependencies ?? {}),
      ...Object.keys(manifest.devDependencies ?? {}),
    ]);

    const violations = collectImportSites().flatMap(
      ({ file, line, specifier }) => {
        if (/^[./]/.test(specifier) || specifier.startsWith("node:")) return [];
        // 宿主耦合由上一条守卫报告，这里不重复计数。
        if (HOST_COUPLING_RULES.some((rule) => rule.matches(specifier)))
          return [];

        const name = packageNameOf(specifier);
        if (declared.has(name)) return [];
        // 未声明的裸依赖在桌面端能解析（提升到 frontend/node_modules 了），
        // 到了 agentre-server 那侧却是 MODULE_NOT_FOUND。
        return [
          `${file}:${line} "${specifier}" —— ${name} 未在 package.json 声明`,
        ];
      },
    );

    expect(violations).toEqual([]);
  });
});
