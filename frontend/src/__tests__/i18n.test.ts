import { describe, expect, it } from "vitest";
import fs from "node:fs";
import path from "node:path";
import ts from "typescript";

import {
  AGENTRE_UI_NAMESPACE,
  agentreUiResources,
} from "@agentre-hub/agentre-ui";

import i18n, { LANGUAGE_STORAGE_KEY, detectInitialLanguage } from "@/i18n";
import enCommon from "@/i18n/locales/en";
import zhCommon from "@/i18n/locales/zh-CN";

type LocaleTree = Record<string, unknown>;

function flattenKeys(value: unknown, prefix = ""): string[] {
  if (!value || typeof value !== "object" || Array.isArray(value)) {
    return prefix ? [prefix] : [];
  }

  return Object.entries(value as LocaleTree).flatMap(([key, child]) => {
    const nextPrefix = prefix ? `${prefix}.${key}` : key;
    return flattenKeys(child, nextPrefix);
  });
}

function writableStorageWithLanguage(language: string | null): Storage & {
  writes: [string, string][];
} {
  let stored = language;
  const writes: [string, string][] = [];

  return {
    writes,
    get length() {
      return stored === null ? 0 : 1;
    },
    clear() {
      stored = null;
    },
    getItem(key: string) {
      return key === LANGUAGE_STORAGE_KEY ? stored : null;
    },
    key(index: number) {
      return index === 0 && stored !== null ? LANGUAGE_STORAGE_KEY : null;
    },
    removeItem(key: string) {
      if (key === LANGUAGE_STORAGE_KEY) stored = null;
    },
    setItem(key: string, value: string) {
      writes.push([key, value]);
      if (key === LANGUAGE_STORAGE_KEY) stored = value;
    },
  };
}

function hasLocaleKey(locale: LocaleTree, key: string): boolean {
  return key.split(".").every((part, index, parts) => {
    const parent = parts.slice(0, index).reduce<unknown>((node, segment) => {
      return node && typeof node === "object"
        ? (node as LocaleTree)[segment]
        : undefined;
    }, locale);

    return Boolean(
      parent && typeof parent === "object" && part in (parent as LocaleTree),
    );
  });
}

function walkSourceFiles(dir: string, out: string[] = []): string[] {
  for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
    const fullPath = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      if (entry.name !== "__tests__" && entry.name !== "i18n") {
        walkSourceFiles(fullPath, out);
      }
      continue;
    }
    if (
      /\.(ts|tsx)$/.test(entry.name) &&
      !/\.(test|spec)\.(ts|tsx)$/.test(entry.name)
    ) {
      out.push(fullPath);
    }
  }
  return out;
}

function walkProductionSourceFiles(): string[] {
  // 共享包 packages/agentre-ui 是本 app 的一部分生产代码(桌面端经 vite alias 直接
  // 吃它的源码),中文硬编码这条红线在包里同样成立。组件正在从 src/ 搬进包,
  // 若这里只扫 src/,搬一个文件就等于让它悄悄脱离守卫。
  return [
    ...walkSourceFiles(path.resolve(process.cwd(), "src")),
    ...walkSourceFiles(path.resolve(process.cwd(), "packages/agentre-ui/src")),
  ];
}

function collectStaticI18nKeys(files: string[]): string[] {
  const keys = new Set<string>();
  const patterns = [
    /(?:^|[^\w.])t\(\s*(["'`])([^"'`$]+)\1/g,
    /i18n\.t\(\s*(["'`])([^"'`$]+)\1/g,
    /i18nKey\s*=\s*(["'`])([^"'`$]+)\1/g,
  ];

  for (const file of files) {
    const source = fs.readFileSync(file, "utf8");
    for (const pattern of patterns) {
      for (const match of source.matchAll(pattern)) {
        keys.add(match[2]);
      }
    }
  }

  return [...keys].filter((key) => !key.includes(":")).sort();
}

function collectStaticCommonI18nKeys(): string[] {
  return collectStaticI18nKeys(
    walkSourceFiles(path.resolve(process.cwd(), "src")),
  );
}

/**
 * 反向守卫：`common` 语言包里不应存在「没人用」的 key。
 *
 * 上面那条正向守卫只覆盖「代码 → 语言包」这个方向：它保证代码引用的 key 都在，
 * 却对**删多了**一无所知——把还在用的 key 删掉同样全绿，代价是界面直接印出
 * `chatPanel.foo` 这样的字面量。这里补上反方向：
 *
 *   语言包里每个叶子 key，都必须能被「代码里的引用」或「显式白名单」覆盖。
 *
 * 「代码里的引用」不是只算 `t("…")`——mapping 表把 key 当普通字符串存着
 * （`not-chattable/mapping.ts` 的 `copyKey`），组件也可能把 `t` 交给别的模块去拼
 * （`debug` 面板的 `translate` 回调）。所以这里同时收集**整份宿主源码文本里
 * 出现过的点分路径**，并允许它带宿主前缀（`enCommon.a.b` 覆盖 bundle 里的 `a.b`）。
 * 测试与注释也算命中——守卫的目标是不误报，多留一条 key 比误红一条划算。
 *
 * 覆盖不到的那些必须逐条列进白名单，且每条注明是**哪段代码在拼**。白名单宁可
 * 宽一点：一条误报会逼后来的人把整条守卫关掉，那比没有守卫更糟。
 */

/**
 * 运行期拼出来的 key 前缀——静态字面量里永远查不到完整 key。
 * 每条前缀后面的注释指出拼它的代码位置。
 */
const DYNAMIC_KEY_PREFIXES = [
  // components/agentre/hooks-page/script-tab.tsx / hooks-page-header.tsx:
  //   t(`hooks.interp.${opt.key}`) / t(`hooks.status.${hookStatus(…)}`)
  "hooks.interp.",
  "hooks.status.",
  // components/agentre/hooks-page.tsx: t(`hooks.tabs.${tab}`)
  "hooks.tabs.",
  // not-chattable/mapping.ts 的 copyKey + not-chattable-dialog.tsx:
  //   t(`${reasonKey}.title`) / t(`${reasonKey}.description`)
  "chatPage.notChattable.reasons.",
  // not-chattable/not-chattable-dialog.tsx:
  //   t(`chatPage.notChattable.chain.states.${backendState}`)
  "chatPage.notChattable.chain.states.",
  // session-exec-target.tsx: t(`chatPanel.execTarget.reasons.${key}`)
  "chatPanel.execTarget.reasons.",
  // chat-tabs/tab-tooltip.tsx: t(`chatTabs.status.${status}`)
  "chatTabs.status.",
  // data-backup/import-preview-dialog.tsx: t(`dataBackup.actions.${action}`)
  "dataBackup.actions.",
  // data-backup/import-result-dialog.tsx: t(`dataBackup.importResult.${k}`)
  "dataBackup.importResult.",
  // data-backup/import-preview-dialog.tsx / export-section.tsx:
  //   t(`dataBackup.scopes.${scope}`)
  "dataBackup.scopes.",
  // board/exec-target-pill.tsx: t(`issues.exec.kinds.${candidate.kind}`)
  "issues.exec.kinds.",
  // lib/turn-notify.ts: t(`notify.body.${kind}`)
  "notify.body.",
  // task-progress/task-progress-bar.tsx: t(`taskProgress.status.${task.status}`)
  "taskProgress.status.",
];

/**
 * 不是动态拼的，而是被一条**跨 namespace 的陈旧断言**钉住的键。
 *
 * session-index-chrome.test.tsx 已经从共享包 `@agentre-hub/agentre-ui` 取
 * `AxisPicker`（它只认 agentreUi 那一份文案），却仍然拿宿主 `enCommon` / `zhCommon`
 * 的 `sessionIndex.axis.*` 当期望值（`enCommon.sessionIndex.axis.title`，以及经中间
 * 变量访问的 `axis.project` / `axis.agent` / `axis.time`）。所以这四条宿主键在生产
 * 里没有读者，删掉却会让那条断言崩。等断言改读 `agentreUiResources` 后再删，
 * 在那之前由这条白名单显式兜住。
 */
const TEST_PINNED_KEY_PREFIXES = ["sessionIndex.axis."];

/** 含测试的宿主源码文本——key 可能只被断言或 fixtures 提到。 */
function collectReferencedKeyPaths(): Set<string> {
  const paths = new Set<string>();
  // `enCommon.sessionIndex.axis.title` 这种宿主前缀，以及 mapping 表里裸写的
  // `chatPage.notChattable.reasons.noBackend`，都会被这一条 token 正则整个吃掉。
  const tokenPattern = /[A-Za-z_$][\w$]*(?:\.[\w$]+)+/g;

  for (const file of collectHostSourceFiles()) {
    const source = fs.readFileSync(file, "utf8");
    for (const match of source.matchAll(tokenPattern)) {
      const token = match[0];
      paths.add(token);
      // 逐级剥掉前缀：宿主写的 `enCommon.a.b` 要能覆盖 bundle 里的 `a.b`。
      let dot = token.indexOf(".");
      while (dot !== -1) {
        paths.add(token.slice(dot + 1));
        dot = token.indexOf(".", dot + 1);
      }
    }
  }

  return paths;
}

/** 宿主 `src` 下的所有 ts/tsx，含 `__tests__`，但排除 `src/i18n` 本身。 */
function collectHostSourceFiles(): string[] {
  const files: string[] = [];

  const walk = (dir: string): void => {
    for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
      const fullPath = path.join(dir, entry.name);
      if (entry.isDirectory()) {
        if (entry.name !== "i18n") walk(fullPath);
        continue;
      }
      if (/\.(ts|tsx)$/.test(entry.name)) files.push(fullPath);
    }
  };

  walk(path.resolve(process.cwd(), "src"));
  return files;
}

function isCommonKeyReferenced(
  key: string,
  staticKeys: Set<string>,
  referencedPaths: Set<string>,
): boolean {
  if (staticKeys.has(key)) return true;
  if (referencedPaths.has(key)) return true;
  if (DYNAMIC_KEY_PREFIXES.some((prefix) => key.startsWith(prefix))) {
    return true;
  }
  return TEST_PINNED_KEY_PREFIXES.some((prefix) => key.startsWith(prefix));
}

function collectProductionHanStringLiterals(): string[] {
  const han = /\p{Script=Han}/u;
  const findings: string[] = [];

  for (const file of walkProductionSourceFiles()) {
    const source = fs.readFileSync(file, "utf8");
    const sourceFile = ts.createSourceFile(
      file,
      source,
      ts.ScriptTarget.Latest,
      true,
      file.endsWith(".tsx") ? ts.ScriptKind.TSX : ts.ScriptKind.TS,
    );

    const record = (node: ts.Node, value: string) => {
      if (!han.test(value)) return;
      const pos = sourceFile.getLineAndCharacterOfPosition(
        node.getStart(sourceFile),
      );
      findings.push(
        `${path.relative(process.cwd(), file)}:${pos.line + 1} ${value
          .replace(/\s+/g, " ")
          .slice(0, 160)}`,
      );
    };

    const visit = (node: ts.Node): void => {
      if (
        ts.isStringLiteral(node) ||
        ts.isNoSubstitutionTemplateLiteral(node)
      ) {
        record(node, node.text);
      } else if (ts.isJsxText(node)) {
        record(node, node.getText(sourceFile).trim());
      } else if (ts.isTemplateExpression(node)) {
        record(node, source.slice(node.getStart(sourceFile), node.getEnd()));
      }
      ts.forEachChild(node, visit);
    };

    visit(sourceFile);
  }

  return findings.sort();
}

const disallowedProductUiLiterals = [
  { value: "New chat with", mode: "includes" },
  { value: "New project chat with", mode: "includes" },
  { value: "Write tool call", mode: "includes" },
  { value: "Endpoint / Key", mode: "exact" },
  { value: "Bot Token", mode: "exact" },
  { value: "Files", mode: "exact" },
  { value: "OFF", mode: "exact" },
  { value: "Outline", mode: "exact" },
  { value: "Permission Mode", mode: "exact" },
  { value: "Provider Key", mode: "exact" },
  { value: "Slack Bot Token", mode: "exact" },
  { value: "Webhook Secret", mode: "exact" },
  { value: "Webhook URL", mode: "exact" },
  { value: "ACTIVE", mode: "exact" },
  { value: "ANSWERED", mode: "exact" },
  { value: "DELETED", mode: "exact" },
  { value: "Hook", mode: "exact" },
  { value: "Leader", mode: "exact" },
  { value: "NEW", mode: "exact" },
  { value: "Projects", mode: "exact" },
  { value: "SKIPPED", mode: "exact" },
  { value: "Write", mode: "exact" },
  { value: "agent-backend", mode: "exact" },
  { value: "agent-description", mode: "exact" },
  { value: "agent-name", mode: "exact" },
  { value: "agent-prompt", mode: "exact" },
  { value: "dept-description", mode: "exact" },
  { value: "dept-lead", mode: "exact" },
  { value: "dept-name", mode: "exact" },
  { value: "dept-parent", mode: "exact" },
  { value: "fallback", mode: "exact" },
  { value: "enabled", mode: "exact" },
  { value: "new-agent-backend", mode: "exact" },
  { value: "new-agent-description", mode: "exact" },
  { value: "new-agent-name", mode: "exact" },
  { value: "new-agent-placement", mode: "exact" },
  { value: "new-dept-name", mode: "exact" },
  { value: "new-dept-parent", mode: "exact" },
  { value: "paused", mode: "exact" },
] as const;

function collectProductionProductUiLiterals(): string[] {
  const visibleAttributes = new Set([
    "alt",
    "aria-description",
    "aria-label",
    "aria-valuetext",
    "label",
    "placeholder",
    "title",
  ]);
  const findings: string[] = [];

  for (const file of walkProductionSourceFiles().filter((sourceFile) =>
    sourceFile.endsWith(".tsx"),
  )) {
    const source = fs.readFileSync(file, "utf8");
    const sourceFile = ts.createSourceFile(
      file,
      source,
      ts.ScriptTarget.Latest,
      true,
      ts.ScriptKind.TSX,
    );

    const record = (node: ts.Node, value: string) => {
      const normalized = value.replace(/\s+/g, " ").trim();
      if (!normalized) return;

      const matched = disallowedProductUiLiterals.find((literal) =>
        literal.mode === "exact"
          ? normalized === literal.value
          : normalized.includes(literal.value),
      );
      if (!matched) return;

      const pos = sourceFile.getLineAndCharacterOfPosition(
        node.getStart(sourceFile),
      );
      findings.push(
        `${path.relative(process.cwd(), file)}:${pos.line + 1} ${normalized}`,
      );
    };

    const visit = (node: ts.Node): void => {
      if (ts.isJsxText(node)) {
        record(node, node.getText(sourceFile));
      } else if (
        ts.isJsxExpression(node) &&
        node.expression &&
        (ts.isStringLiteral(node.expression) ||
          ts.isNoSubstitutionTemplateLiteral(node.expression) ||
          ts.isTemplateExpression(node.expression))
      ) {
        record(
          node,
          source.slice(
            node.expression.getStart(sourceFile),
            node.expression.end,
          ),
        );
      } else if (
        ts.isJsxAttribute(node) &&
        ts.isIdentifier(node.name) &&
        visibleAttributes.has(node.name.text) &&
        node.initializer
      ) {
        if (ts.isStringLiteral(node.initializer)) {
          record(node.initializer, node.initializer.text);
        } else if (
          ts.isJsxExpression(node.initializer) &&
          node.initializer.expression &&
          (ts.isStringLiteral(node.initializer.expression) ||
            ts.isNoSubstitutionTemplateLiteral(node.initializer.expression) ||
            ts.isTemplateExpression(node.initializer.expression))
        ) {
          const expression = node.initializer.expression;
          record(
            expression,
            source.slice(expression.getStart(sourceFile), expression.end),
          );
        }
      }
      ts.forEachChild(node, visit);
    };

    visit(sourceFile);
  }

  return findings.sort();
}

const blockedI18nArtifacts = {
  localizerFile: ["dom", "localizer"].join("-") + ".ts",
  namespace: ["source", "Text"].join(""),
  sourceFile: ["source", "text"].join("-") + ".json",
  translatedAttribute: ["data", "i18n", "ignore"].join("-"),
};

const shellAndSettingsKeys = [
  "app.commandPalette.placeholder",
  "app.commandPalette.open",
  "app.navigationLabel",
  "app.window.close",
  "app.window.maximize",
  "app.window.minimize",
  "nav.chat",
  "nav.hooks",
  "nav.issues",
  "nav.org",
  "nav.projects",
  "nav.settings",
  "settings.agentBackend.description",
  "settings.agentBackend.title",
  "settings.appearance.colorMode.title",
  "settings.appearance.description",
  "settings.appearance.themeMode.label",
  "settings.appearance.title",
  "settings.localProxy.description",
  "settings.localProxy.title",
  "settings.llmProvider.description",
  "settings.llmProvider.title",
  "settings.nav.about",
  "settings.nav.dataBackup",
  "settings.nav.engine",
  "settings.nav.general",
  "settings.nav.integrations",
  "settings.nav.keyboardShortcuts",
  "settings.nav.localProxy",
  "settings.nav.mcpServers",
  "settings.nav.notifications",
  "settings.nav.remoteDevices",
  "settings.nav.skillsTools",
  "settings.nav.versionLogs",
  "settings.notifications.pageTitle",
  "settings.notifications.enableLabel",
  "settings.notifications.onlyWhenUnfocusedLabel",
  "settings.notifications.systemLabel",
  "settings.notifications.ruleDesc",
  "notify.body.done",
  "notify.openSession",
  "notify.dismiss",
  "notify.justNow",
  "settings.underConstruction.mcpServers.description",
  "settings.underConstruction.mcpServers.title",
  "settings.underConstruction.skillsTools.description",
  "settings.underConstruction.skillsTools.title",
  "underConstruction.badge",
  "underConstruction.planning",
  "underConstruction.progressLabel",
  // 切换按钮那几条（lightMode / darkMode / systemWithResolved / toggle /
  // toggleTitle）随 ThemeToggle 一起进了共享包的 agentreUi bundle；留下的三条是
  // 设置页「外观」下拉自己的文案。
  "theme.dark",
  "theme.light",
  "theme.system",
];

describe("i18n resources", () => {
  it("Given zh-CN and en common locales, When keys are flattened, Then both languages expose the same keys", () => {
    const zhKeys = flattenKeys(zhCommon).sort();
    const enKeys = flattenKeys(enCommon).sort();

    expect(zhKeys.filter((key) => !enKeys.includes(key))).toEqual([]);
    expect(enKeys.filter((key) => !zhKeys.includes(key))).toEqual([]);
  });

  it("Given the shared agentre-ui package ships its own locales, When the host instance is initialized, Then they are merged under their own namespace and the host common namespace is untouched", () => {
    for (const language of ["zh-CN", "en"] as const) {
      // 包的 key 进得来 —— CodeBlock 已随组件搬进包,这两条文案现在只有包有。
      expect(
        i18n.getResource(language, AGENTRE_UI_NAMESPACE, "codeBlock.copyDone"),
      ).toBe(agentreUiResources[language].codeBlock.copyDone);
      expect(
        i18n.getResource(
          language,
          AGENTRE_UI_NAMESPACE,
          "codeBlock.copyFailed",
        ),
      ).toBe(agentreUiResources[language].codeBlock.copyFailed);
      // 且没有顺手灌进 common —— 两棵树各自独立,包的资源不会漏进宿主 namespace。
      expect(
        i18n.getResource(language, "common", "codeBlock.copyDone"),
      ).toBeUndefined();
    }
  });

  it("Given App shell and settings UI translation keys, When locales are checked, Then both languages provide every key", () => {
    expect(
      shellAndSettingsKeys.filter((key) => !hasLocaleKey(zhCommon, key)),
    ).toEqual([]);
    expect(
      shellAndSettingsKeys.filter((key) => !hasLocaleKey(enCommon, key)),
    ).toEqual([]);
  });

  it("Given static common translation calls, When locales are checked, Then both languages provide every key", () => {
    const keys = collectStaticCommonI18nKeys();

    expect(keys.filter((key) => !hasLocaleKey(zhCommon, key))).toEqual([]);
    expect(keys.filter((key) => !hasLocaleKey(enCommon, key))).toEqual([]);
  });

  it("Given the common locale bundle, When every leaf key is inspected, Then each one is referenced by code or an explicit whitelist", () => {
    const staticKeys = collectStaticCommonI18nKeys();

    // 守卫自证「不空过」：正则或过滤哪天写错，静态 key 会塌成 0 条，
    // 那样下面的差集自动为空、守卫静默全绿。
    expect(staticKeys.length).toBeGreaterThan(800);

    const staticKeySet = new Set(staticKeys);
    const referencedPaths = collectReferencedKeyPaths();

    const unused = flattenKeys(enCommon).filter((key) => {
      if (isCommonKeyReferenced(key, staticKeySet, referencedPaths)) {
        return false;
      }
      // 复数键：语言包里是 `x_one` / `x_other`，代码里调的是 base `x`。
      const base = key.replace(/_(?:one|other)$/, "");
      return (
        base === key ||
        !isCommonKeyReferenced(base, staticKeySet, referencedPaths)
      );
    });

    expect(
      unused,
      "这些 key 在宿主代码里已经没有读者，界面不会用到；删掉它们，或把「哪段代码在拼」写进白名单",
    ).toEqual([]);
  });

  it("Given explicit React i18n, When production string literals are inspected, Then no Chinese UI copy is hardcoded outside locale files", () => {
    expect(collectProductionHanStringLiterals()).toEqual([]);
  });

  it("Given explicit React i18n, When production JSX is inspected, Then known product UI copy is not hardcoded", () => {
    expect(collectProductionProductUiLiterals()).toEqual([]);
  });

  it("Given explicit React i18n, When production sources are inspected, Then auxiliary i18n entry files are absent", () => {
    const sourceRoot = path.resolve(process.cwd(), "src");
    const forbiddenFiles = [
      path.join(sourceRoot, "i18n", blockedI18nArtifacts.localizerFile),
      path.join(
        sourceRoot,
        "i18n",
        "locales",
        "en",
        blockedI18nArtifacts.sourceFile,
      ),
      path.join(
        sourceRoot,
        "i18n",
        "locales",
        "zh-CN",
        blockedI18nArtifacts.sourceFile,
      ),
    ];

    expect(
      forbiddenFiles
        .filter((file) => fs.existsSync(file))
        .map((file) => path.relative(process.cwd(), file)),
    ).toEqual([]);
  });

  it("Given explicit React i18n, When i18n setup is inspected, Then only the expected text namespace is registered", () => {
    const source = fs.readFileSync(
      path.resolve(process.cwd(), "src/i18n/index.ts"),
      "utf8",
    );

    expect(source).not.toContain(blockedI18nArtifacts.namespace);
  });

  it("Given dynamic content is rendered directly, When production sources are inspected, Then no auxiliary localization attributes remain", () => {
    const filesWithIgnore = walkProductionSourceFiles()
      .filter((file) =>
        fs
          .readFileSync(file, "utf8")
          .includes(blockedI18nArtifacts.translatedAttribute),
      )
      .map((file) => path.relative(process.cwd(), file));

    expect(filesWithIgnore).toEqual([]);
  });
});

describe("detectInitialLanguage", () => {
  it("Given a supported stored language, When language is detected, Then the stored preference wins and is not overwritten", () => {
    const storage = writableStorageWithLanguage("en");
    const detected = detectInitialLanguage({
      navigatorLanguage: "zh-CN",
      storage,
    });

    expect(detected).toBe("en");
    expect(storage.writes).toEqual([]);
  });

  it("Given zh-CN is the stored language, When language is detected, Then the explicit Chinese preference wins", () => {
    const storage = writableStorageWithLanguage("zh-CN");
    const detected = detectInitialLanguage({
      navigatorLanguage: "en-US",
      storage,
    });

    expect(detected).toBe("zh-CN");
    expect(storage.writes).toEqual([]);
  });

  it("Given no stored language and a supported Chinese system locale, When language is detected, Then zh-CN is selected and stored", () => {
    const storage = writableStorageWithLanguage(null);
    const detected = detectInitialLanguage({
      navigatorLanguages: ["zh-CN", "en-US"],
      storage,
    });

    expect(detected).toBe("zh-CN");
    expect(storage.writes).toEqual([[LANGUAGE_STORAGE_KEY, "zh-CN"]]);
  });

  it("Given no stored language and an unsupported Chinese system locale, When language is detected, Then English is selected and stored", () => {
    const storage = writableStorageWithLanguage(null);
    const detected = detectInitialLanguage({
      navigatorLanguage: "zh-Hant-HK",
      storage,
    });

    expect(detected).toBe("en");
    expect(storage.writes).toEqual([[LANGUAGE_STORAGE_KEY, "en"]]);
  });

  it("Given an unsupported stored language and a non-supported system locale, When language is detected, Then English is used and stored", () => {
    const storage = writableStorageWithLanguage("ja");
    const detected = detectInitialLanguage({
      navigatorLanguage: "fr-FR",
      storage,
    });

    expect(detected).toBe("en");
    expect(storage.writes).toEqual([[LANGUAGE_STORAGE_KEY, "en"]]);
  });
});
