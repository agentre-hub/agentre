import { render, screen } from "@testing-library/react";
import { createInstance } from "i18next";
import {
  I18nextProvider,
  initReactI18next,
  useTranslation,
} from "react-i18next";
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
