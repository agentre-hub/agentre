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
    if (manifest.name === "@agentre-hub/agentre-ui") return candidate;
  }

  throw new Error("@agentre-hub/agentre-ui package root not found");
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
 * 反向守卫：包的 bundle 里不应存在「没人用」的 key。
 *
 * 上面那条 `collectPackageI18nKeys` 只覆盖「代码 → bundle」：它保证包内每个静态
 * `t("…")` 都有文案，却对**删多了**一无所知——把还在用的 key 删掉同样全绿，
 * 代价是宿主（桌面端 / agentre-server）直接显示 key 字面量。这里补反方向。
 *
 * 一个叶子 key 只要满足下面任一条就算「有人用」：
 *   1. 包内生产源码里的静态 `t("…")` / `i18n.t("…")`；
 *   2. 包内任意源码（含测试）里出现过的点分路径；
 *   3. `HOST_CONSUMER_KEYS`：宿主经包 namespace 取的 key（`uiT("…")`）；
 *   4. `DYNAMIC_KEY_PREFIXES`：运行期拼出来的前缀；
 *   5. 复数键：base key 有人用即可。
 *
 * 白名单故意写得宽——一条误报会逼后来的人把守卫关掉，那比没有守卫更糟。
 */

/**
 * 运行期拼出来的 key 前缀——静态字面量里永远查不到完整 key。
 * 每条后面注明是哪段代码在拼。
 */
const DYNAMIC_KEY_PREFIXES = [
  // engine/agent-backends-fields.tsx: t(`agentBackends.approval.options.${opt.value}`)
  "agentBackends.approval.options.",
  // engine/agent-backends-{list,badges}.tsx / agent-backends.tsx:
  //   t(`agentBackends.backendType.${typ}.label`) / `.shortLabel` / `.probe.${state}`
  "agentBackends.backendType.",
  // engine/agent-backends-fields.tsx: t(`agentBackends.reasoning.options.${opt || "default"}`)
  "agentBackends.reasoning.options.",
  // engine/backend-editor/draft.ts: translate(`agentBackends.openclaw.errors.${key}`)
  // （宿主把 `t` 注进来，键仍是包自己的 namespace）
  "agentBackends.openclaw.errors.",
  // project/directory-picker.tsx: t(`directoryPicker.failure.${key}`)
  "directoryPicker.failure.",
  // org/icon-registry.ts: t(`${AGENTRE_UI_NAMESPACE}:iconRegistry.categories.${key}`)
  "iconRegistry.categories.",
  // engine/llm-provider-models/discover-error-panel.tsx:
  //   t(`llmProviders.discover.error.${failure.kind}`) / `.errorTitle.${failure.kind}`
  "llmProviders.discover.error.",
  "llmProviders.discover.errorTitle.",
  // engine/llm-provider-models/{provider-nav,workspace-header,provider-form-fields}.tsx:
  //   t(`llmProviders.providerType.${type}.label`)
  "llmProviders.providerType.",
  // engine/model-target-picker/use-picker-options.ts:
  //   t(`modelTargetPicker.special.${scenario}`)
  "modelTargetPicker.special.",
  // transcript/openclaw-exec-approval/card.tsx:
  //   t(`openclawExecApproval.decision.${value}`) / `.decisionResult.${decision}`
  "openclawExecApproval.decision.",
  "openclawExecApproval.decisionResult.",
  // org/exec-target-reasons.ts: t(`org.agent.execTargets.reasons.${key}`)
  "org.agent.execTargets.reasons.",
  // org/tool-catalog.ts: t(`org.agent.tools.names.${key}`) / `.descriptions.${key}`
  "org.agent.tools.names.",
  "org.agent.tools.descriptions.",
  // project/failure-text.ts: t(`projectSettings.failure.${kind}`)
  "projectSettings.failure.",
  // transcript/tool-approval/card.tsx:
  //   t(`toolApproval.status.${approval.status}`) / t(`toolApproval.tools.${approval.toolName}`)
  "toolApproval.status.",
  "toolApproval.tools.",
];

/**
 * 宿主直接经包 namespace 取的 key（宿主源码里的 `uiT("…")`）。
 *
 * 这些 key 的字符串只出现在宿主的 `src/` 里，包内 `pnpm test` 的 cwd 够不到
 * agentre-server；而桌面端与包同仓，所以每天新加一条 `uiT(...)` 时必须同步补这里。
 * 业务上的所有其它宿主消费都走包内组件的 `useUiTranslation`，已被静态扫描覆盖。
 */
const HOST_CONSUMER_KEYS = [
  // frontend/src/components/agentre/file-preview/file-preview-panel.tsx: uiT("filePreview.panelAria")
  "filePreview.panelAria",
  // frontend/src/components/agentre/org/exec-target-list.tsx:
  //   uiT("org.agent.execTargets.localMachine") / uiT("org.agent.execTargets.reasons.unpaired")
  "org.agent.execTargets.localMachine",
  "org.agent.execTargets.reasons.unpaired",
];

/** 包内所有 ts/tsx 里出现过的点分路径（含测试，排除 `src/i18n` 自身的示例）。 */
function collectReferencedPackageKeyPaths(): Set<string> {
  const paths = new Set(HOST_CONSUMER_KEYS);
  const tokenPattern = /[A-Za-z_$][\w$]*(?:\.[\w$]+)+/g;
  const packageRoot = locatePackageRoot();
  const sourceRoot = join(packageRoot, "src");

  const walk = (dir: string): void => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      const fullPath = join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name !== "i18n") walk(fullPath);
        continue;
      }
      if (!/\.(ts|tsx)$/.test(entry.name)) continue;
      const source = readFileSync(fullPath, "utf8");
      for (const match of source.matchAll(tokenPattern)) {
        const token = match[0];
        paths.add(token);
        // 逐级剥前缀：`agentreUiResources.en.agentBackends.x` 要能覆盖 `agentBackends.x`。
        let dot = token.indexOf(".");
        while (dot !== -1) {
          paths.add(token.slice(dot + 1));
          dot = token.indexOf(".", dot + 1);
        }
      }
    }
  };

  walk(sourceRoot);
  return paths;
}

function isPackageKeyReferenced(
  key: string,
  staticKeys: Set<string>,
  referencedPaths: Set<string>,
): boolean {
  if (staticKeys.has(key)) return true;
  if (referencedPaths.has(key)) return true;
  return DYNAMIC_KEY_PREFIXES.some((prefix) => key.startsWith(prefix));
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
  const localeModules = (language: "en" | "zh-CN") => {
    const localeDir = join(
      locatePackageRoot(),
      "src",
      "i18n",
      "locales",
      language,
    );

    return readdirSync(localeDir)
      .filter((fileName) => fileName.endsWith(".json"))
      .sort();
  };

  it("Given locale bundles split by domain, When language directories are compared, Then both languages ship the same module files", () => {
    expect(localeModules("zh-CN")).toEqual(localeModules("en"));
  });

  it.each(["zh-CN", "en"] as const)(
    "Given the %s locale modules, When top-level ownership is inspected, Then every key has exactly one owner and the barrel merges every module",
    (language) => {
      const localeDir = join(
        locatePackageRoot(),
        "src",
        "i18n",
        "locales",
        language,
      );
      const owners = new Map<string, string[]>();

      for (const fileName of localeModules(language)) {
        const module = JSON.parse(
          readFileSync(join(localeDir, fileName), "utf8"),
        ) as LocaleTree;
        for (const key of Object.keys(module)) {
          owners.set(key, [...(owners.get(key) ?? []), fileName]);
        }
      }

      expect(
        [...owners.entries()]
          .filter(([, files]) => files.length > 1)
          .map(([key, files]) => `${key}: ${files.join(", ")}`),
      ).toEqual([]);
      expect(Object.keys(agentreUiResources[language]).sort()).toEqual(
        [...owners.keys()].sort(),
      );
    },
  );

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

  it("Given the exported bundles, When every leaf key is inspected, Then each one is referenced by package code, a host consumer or an explicit dynamic whitelist", () => {
    const sites = collectPackageI18nKeys();

    // 守卫自证「不空过」：AST 遍历或过滤哪天写错，静态 key 会塌成 0 条，
    // 下面的差集自动为空、守卫静默全绿。
    expect(sites.length).toBeGreaterThan(500);

    const staticKeys = new Set(sites.map(({ key }) => key));
    const referencedPaths = collectReferencedPackageKeyPaths();

    const unused = flattenKeys(agentreUiResources.en).filter((key) => {
      if (isPackageKeyReferenced(key, staticKeys, referencedPaths)) {
        return false;
      }
      // 复数键：bundle 里是 `x_one` / `x_other`，代码里调的是 base `x`。
      const base = key.replace(/_(?:one|other)$/, "");
      return (
        base === key ||
        !isPackageKeyReferenced(base, staticKeys, referencedPaths)
      );
    });

    expect(
      unused,
      "这些 key 在包内与宿主都没有读者，删掉它们，或把「哪段代码在拼」写进白名单",
    ).toEqual([]);
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
