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

  it("Given the Claude usage poll interval, When quota copy is checked, Then both languages describe the same five-minute interval", () => {
    const ccUsageSource = fs.readFileSync(
      path.resolve(process.cwd(), "../internal/app/cc_usage.go"),
      "utf8",
    );

    expect(ccUsageSource).toContain(
      "const ccUsagePollInterval = 5 * time.Minute",
    );
    expect(zhCommon.chat.quota.panel.usedNote).toBe(
      "百分比为已用比例 · 每 5 分钟刷新",
    );
    expect(enCommon.chat.quota.panel.usedNote).toBe(
      "Percentages are usage · refreshed every 5 minutes",
    );
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
