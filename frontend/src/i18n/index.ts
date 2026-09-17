// 走 `/i18n` 窄入口而不是包的 barrel:这个模块是**测试 setup 最先加载**的东西
// (src/__tests__/setup.ts → @/i18n),从 barrel 进来会把整个包(含 sonner 的
// clipboard-toast)在任何 `vi.mock("sonner")` 注册之前就装进模块缓存,之后所有
// 打在 sonner 上的桩都会静默失效 —— 组件照常调真 toast,断言看到 0 次调用。
// 语言包本来也不需要组件树,窄入口同时让 agentre-server 只取文案而不拖渲染器。
import {
  AGENTRE_UI_NAMESPACE,
  agentreUiResources,
} from "@agentre-hub/agentre-ui/i18n";
import i18n from "i18next";
import { initReactI18next } from "react-i18next";

import enCommon from "./locales/en";
import zhCommon from "./locales/zh-CN";

export const LANGUAGE_STORAGE_KEY = "agentre.language";

export type SupportedLanguage = "zh-CN" | "en";

/**
 * 共享包 `@agentre-hub/agentre-ui` 自带语言包，但**不建自己的 i18next 实例** ——
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

function getBrowserStorage(): Storage | null {
  if (typeof window === "undefined") return null;

  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function readStoredLanguage(): SupportedLanguage | null {
  const storage = getBrowserStorage();
  if (!storage) return null;

  try {
    return normalizeStoredLanguage(storage.getItem(LANGUAGE_STORAGE_KEY));
  } catch {
    return null;
  }
}

function writeStoredLanguage(language: SupportedLanguage): void {
  const storage = getBrowserStorage();
  if (!storage) return;

  try {
    storage.setItem(LANGUAGE_STORAGE_KEY, language);
  } catch {
    // localStorage 不可写（隐私模式 / 配额）不影响本次选择。
  }
}

function detectNavigatorLanguage(): SupportedLanguage {
  if (typeof navigator === "undefined") return "en";

  const candidates = [...(navigator.languages ?? []), navigator.language];
  for (const candidate of candidates) {
    const normalized = (candidate ?? "").trim().toLowerCase();
    if (
      normalized === "zh" ||
      normalized === "zh-cn" ||
      normalized.startsWith("zh-hans")
    ) {
      return "zh-CN";
    }
    if (normalized === "en" || normalized.startsWith("en-")) return "en";
  }

  return "en";
}

export function detectInitialLanguage(): SupportedLanguage {
  const storedLanguage = readStoredLanguage();
  if (storedLanguage) return storedLanguage;

  const detectedLanguage = detectNavigatorLanguage();
  writeStoredLanguage(detectedLanguage);
  return detectedLanguage;
}

i18n.use(initReactI18next).init({
  defaultNS: "common",
  fallbackLng: "en",
  interpolation: {
    escapeValue: false,
  },
  lng: detectInitialLanguage(),
  resources,
  react: {
    useSuspense: false,
  },
});

export default i18n;
