import { render, screen } from "@testing-library/react";
import { createInstance } from "i18next";
import { existsSync, readFileSync, readdirSync } from "node:fs";
import { join, relative, resolve } from "node:path";
import {
  I18nextProvider,
  initReactI18next,
  useTranslation,
} from "react-i18next";
import ts from "typescript";
import { describe, expect, it } from "vitest";

import {
  AGENTRE_UI_NAMESPACE,
  agentreUiResources,
  useUiTranslation,
} from "./index";

type LocaleTree = { [key: string]: string | LocaleTree };

function flattenKeys(value: LocaleTree, prefix = ""): string[] {
  return Object.entries(value).flatMap(([key, child]) => {
    const path = prefix ? `${prefix}.${key}` : key;
    return typeof child === "string" ? [path] : flattenKeys(child, path);
  });
}

function hasLocaleKey(bundle: LocaleTree, key: string): boolean {
  const value = key
    .split(".")
    .reduce<unknown>((node, part) => (node as LocaleTree)?.[part], bundle);

  return typeof value === "string";
}

/**
 * 两个入口都要能跑：宿主 vitest 的 cwd 是 `frontend/`，包自己的 `pnpm test`
 * 的 cwd 是 `frontend/packages/agentre-ui/`。用 package.json 的 name 判别，
 * 而不是赌某个目录存在（与 boundary.test.ts 同一套定位法）。
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
    if (manifest.name === "@agentre-ai/agentre-ui") return candidate;
  }

  throw new Error("@agentre-ai/agentre-ui package root not found");
}

function walkSourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const fullPath = join(dir, entry.name);
    if (entry.isDirectory()) {
      walkSourceFiles(fullPath, out);
      continue;
    }
    // 只看生产源码：用例与夹具里的 `t("…")` 是断言数据，不是要落地的文案。
    if (/\.(test|spec)\.(ts|tsx)$/.test(entry.name)) continue;
    if (/__testing__/.test(fullPath)) continue;
    if (/\.(ts|tsx)$/.test(entry.name)) out.push(fullPath);
  }
  return out;
}

/**
 * 走 AST 而不是正则扫文本。第一版用的是正则，结果把 `i18n/index.ts` 注释里
 * 举例用的 `` `t("...")` `` 当成了真 key —— 一个会报假 key 的守卫比没有守卫更糟。
 * 改成只认**真正的 `t(...)` 调用节点上的字符串字面量实参**：注释和文档示例
 * 天然不在 AST 里，也不需要靠"剥注释"那类会带来假阴性的启发式。
 *
 * 只认字面量 key —— 模板串 / 变量拼出来的 key 静态查不了，交给用例覆盖。
 */
function collectPackageI18nKeys(): {
  file: string;
  line: number;
  key: string;
}[] {
  const packageRoot = locatePackageRoot();
  const sites: { file: string; line: number; key: string }[] = [];

  for (const file of walkSourceFiles(join(packageRoot, "src"))) {
    const source = readFileSync(file, "utf8");
    const sourceFile = ts.createSourceFile(
      file,
      source,
      ts.ScriptTarget.Latest,
      true,
      file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    );

    const visit = (node: ts.Node) => {
      if (ts.isCallExpression(node)) {
        const callee = node.expression;
        // `t(...)` 与 `i18n.t(...)` 都算；`foo.bar.t` 这类也一并接受，
        // 反正取到的实参必须是 bundle 里的 key。
        const isTranslateCall =
          (ts.isIdentifier(callee) && callee.text === "t") ||
          (ts.isPropertyAccessExpression(callee) && callee.name.text === "t");
        const [first] = node.arguments;

        if (isTranslateCall && first && ts.isStringLiteral(first)) {
          // 带命名空间前缀的 key（"ns:foo"）不归本 bundle 管。
          if (!first.text.includes(":")) {
            sites.push({
              file: relative(packageRoot, file),
              line:
                sourceFile.getLineAndCharacterOfPosition(
                  first.getStart(sourceFile),
                ).line + 1,
              key: first.text,
            });
          }
        }
      }
      ts.forEachChild(node, visit);
    };

    visit(sourceFile);
  }

  return sites;
}

/**
 * 模拟宿主：实例是宿主建的，包只把 bundle 交出去。断言的是**合并之后**的行为，
 * 而不是「两个 JSON 文件长得一样」——后者证明不了包内组件真的取得到文案。
 */
function createHostInstance(language: string) {
  const instance = createInstance();

  instance.use(initReactI18next).init({
    defaultNS: "common",
    fallbackLng: "en",
    interpolation: { escapeValue: false },
    lng: language,
    resources: {
      "zh-CN": {
        // 宿主自己的 common 里存在同名 key 路径：搬迁期间宿主与包各有一份，
        // 这两条断言就是在证明它们互不覆盖。
        common: { codeBlock: { copyDone: "宿主的已复制代码" } },
        [AGENTRE_UI_NAMESPACE]: agentreUiResources["zh-CN"],
      },
      en: {
        common: { codeBlock: { copyDone: "Host code copied" } },
        [AGENTRE_UI_NAMESPACE]: agentreUiResources.en,
      },
    },
    react: { useSuspense: false },
  });

  return instance;
}

function PackageCopy() {
  const { t } = useUiTranslation();
  return <span data-testid="package">{t("codeBlock.copyDone")}</span>;
}

function HostCopy() {
  const { t } = useTranslation();
  return <span data-testid="host">{t("codeBlock.copyDone")}</span>;
}

describe("agentre-ui locale bundles", () => {
  it("Given zh-CN and en bundles, When keys are flattened, Then both languages expose the same keys", () => {
    const zhKeys = flattenKeys(agentreUiResources["zh-CN"]).sort();
    const enKeys = flattenKeys(agentreUiResources.en).sort();

    expect(zhKeys).toEqual(enKeys);
    expect(zhKeys.length).toBeGreaterThan(0);
  });

  it("Given the exported bundles, When leaves are inspected, Then no translation is left empty", () => {
    const empty = Object.entries(agentreUiResources).flatMap(
      ([language, bundle]) =>
        flattenKeys(bundle)
          .filter((key) => {
            const value = key
              .split(".")
              .reduce<unknown>(
                (node, part) => (node as LocaleTree)?.[part],
                bundle,
              );
            return typeof value !== "string" || value.trim() === "";
          })
          .map((key) => `${language}:${key}`),
    );

    expect(empty).toEqual([]);
  });

  /**
   * 包里每一条静态 `t("…")` 都必须在两份 bundle 里都有对应文案。
   *
   * 宿主那侧的同类守卫（`src/__tests__/i18n.test.ts` 的
   * `collectStaticCommonI18nKeys`）**只扫 `src`**，扫不到这里，所以组件从
   * `src/` 搬进包时，只要漏搬了 key，桌面端反而看不出来 —— 宿主的 common
   * 命名空间里旧 key 还在，`useTranslation()` 照样取得到。等到 agentre-server
   * 装上这个包（它只有包自带的 bundle），界面才会成片显示原始 key。
   *
   * 这条守卫把那个延迟到消费方才暴露的失败拉回本仓库。
   */
  it("Given package sources, When static t() keys are collected, Then every key exists in both bundles", () => {
    const missing = collectPackageI18nKeys().flatMap(({ file, line, key }) =>
      (["zh-CN", "en"] as const)
        .filter((language) => !hasLocaleKey(agentreUiResources[language], key))
        .map((language) => `${file}:${line} "${key}" —— 缺 ${language}`),
    );

    expect(missing).toEqual([]);
  });
});

describe("useUiTranslation", () => {
  it("Given a host-owned i18n instance, When the package namespace is merged in, Then package copy resolves without touching the host common namespace", () => {
    const instance = createHostInstance("zh-CN");

    render(
      <I18nextProvider i18n={instance}>
        <PackageCopy />
        <HostCopy />
      </I18nextProvider>,
    );

    expect(screen.getByTestId("package").textContent).toBe("已复制代码");
    expect(screen.getByTestId("host").textContent).toBe("宿主的已复制代码");
  });

  it("Given the host switches language, When the package renders, Then it follows the host instance instead of keeping its own state", () => {
    const instance = createHostInstance("en");

    render(
      <I18nextProvider i18n={instance}>
        <PackageCopy />
      </I18nextProvider>,
    );

    expect(screen.getByTestId("package").textContent).toBe("Code copied");
  });
});
