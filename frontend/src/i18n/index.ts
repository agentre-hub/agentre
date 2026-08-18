// 走 `/i18n` 窄入口而不是包的 barrel:这个模块是**测试 setup 最先加载**的东西
// (src/__tests__/setup.ts → @/i18n),从 barrel 进来会把整个包(含 sonner 的
// clipboard-toast)在任何 `vi.mock("sonner")` 注册之前就装进模块缓存,之后所有
// 打在 sonner 上的桩都会静默失效 —— 组件照常调真 toast,断言看到 0 次调用。
// 语言包本来也不需要组件树,窄入口同时让 agentre-server 只取文案而不拖渲染器。
import {
  AGENTRE_UI_NAMESPACE,
  agentreUiResources,
} from "@agentre-ai/agentre-ui/i18n";
import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import enCommon from "./locales/en";
import zhCommon from "./locales/zh-CN";

export const LANGUAGE_STORAGE_KEY = "agentre.language";

export const supportedLanguages = ["zh-CN", "en"] as const;

export type SupportedLanguage = (typeof supportedLanguages)[number];

type LanguageStorage = Pick<Storage, "getItem"> &
  Partial<Pick<Storage, "setItem">>;

type DetectInitialLanguageOptions = {
  navigatorLanguage?: string | null;
  navigatorLanguages?: readonly string[] | null;
  storage?: LanguageStorage | null;
};

/**
 * 共享包 `@agentre-ai/agentre-ui` 自带语言包，但**不建自己的 i18next 实例** ——
 * 实例只有这一个，包的资源在 init 时并进来。包用独立 namespace，所以它的 key
 * 与宿主 `common` 分属两棵树，谁都覆盖不了谁（对象字面量里重复 ns 键 TS 会直接报错）。
 */
const resources = {
  "zh-CN": {
    common: zhCommon,
    [AGENTRE_UI_NAMESPACE]: agentreUiResources["zh-CN"],
  },
  en: { common: enCommon, [AGENTRE_UI_NAMESPACE]: agentreUiResources.en },
};

function normalizeStoredLanguage(value: string | null | undefined) {
  if (!value) return null;

  const normalized = value.trim().toLowerCase();
  if (normalized === "zh-cn") return "zh-CN";
  if (normalized === "en") return "en";

  return null;
}

function normalizeNavigatorLanguage(value: string | null | undefined) {
  if (!value) return null;

  const normalized = value.trim().toLowerCase();
  if (
    normalized === "zh" ||
    normalized === "zh-cn" ||
    normalized.startsWith("zh-hans")
  ) {
    return "zh-CN";
  }
  if (normalized === "en" || normalized.startsWith("en-")) return "en";

  return null;
}

function languageFromNavigator({
  navigatorLanguage,
  navigatorLanguages,
}: Pick<
  DetectInitialLanguageOptions,
  "navigatorLanguage" | "navigatorLanguages"
>): SupportedLanguage {
  const candidates = [
    ...(navigatorLanguages ?? []),
    ...(navigatorLanguage ? [navigatorLanguage] : []),
  ];

  for (const candidate of candidates) {
    const supportedLanguage = normalizeNavigatorLanguage(candidate);
    if (supportedLanguage) return supportedLanguage;
  }

  return "en";
}

function readStoredLanguage(storage: LanguageStorage | null) {
  if (!storage) return null;

  try {
    return normalizeStoredLanguage(storage.getItem(LANGUAGE_STORAGE_KEY));
  } catch {
    return null;
  }
}

function writeStoredLanguage(
  storage: LanguageStorage | null,
  language: SupportedLanguage,
) {
  if (!storage?.setItem) return;

  try {
    storage.setItem(LANGUAGE_STORAGE_KEY, language);
  } catch {
    return;
  }
}

function getBrowserStorage() {
  if (typeof window === "undefined") return null;

  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function getNavigatorLanguage() {
  if (typeof navigator === "undefined") return null;

  return navigator.language;
}

function getNavigatorLanguages() {
  if (typeof navigator === "undefined") return null;

  return navigator.languages.length > 0 ? navigator.languages : null;
}

export function detectInitialLanguage({
  navigatorLanguage,
  navigatorLanguages,
  storage,
}: DetectInitialLanguageOptions = {}): SupportedLanguage {
  const languageStorage = storage ?? null;
  const storedLanguage = readStoredLanguage(languageStorage);
  if (storedLanguage) return storedLanguage;

  const detectedLanguage = languageFromNavigator({
    navigatorLanguage,
    navigatorLanguages,
  });
  writeStoredLanguage(languageStorage, detectedLanguage);
  return detectedLanguage;
}

i18n.use(initReactI18next).init({
  defaultNS: "common",
  fallbackLng: "en",
  interpolation: {
    escapeValue: false,
  },
  lng: detectInitialLanguage({
    navigatorLanguage: getNavigatorLanguage(),
    navigatorLanguages: getNavigatorLanguages(),
    storage: getBrowserStorage(),
  }),
  resources,
  react: {
    useSuspense: false,
  },
});

export default i18n;
