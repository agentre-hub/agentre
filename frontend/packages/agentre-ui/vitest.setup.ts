import "@testing-library/jest-dom/vitest";
import { cleanup } from "@testing-library/react";
import i18n from "i18next";
import { initReactI18next } from "react-i18next";
import { afterEach } from "vitest";

import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "./src/i18n";

/**
 * 只做 RTL 的用例间清理。
 *
 * RTL 的自动 cleanup 依赖全局 afterEach（`globals: true`），本包没开，所以显式挂。
 * 不挂的话上一个用例的 DOM 会留在 document 里，下一个用例的 getByTestId 撞到
 * 多个同名节点——独立跑包测试时确实炸过一次。
 *
 * 刻意**不**复用宿主的 src/__tests__/setup.ts：那里装的是 wails mock、
 * localStorage 垫片等宿主专属的东西，包按定义不依赖它们
 * （由 src/boundary.test.ts 机械保证）。
 */
afterEach(() => {
  cleanup();
  localStorage.clear();
});

/**
 * 包里有落 localStorage 的东西（`SessionGroup` 的展开偏好、转录的滚动位置），
 * 而 happy-dom 在本仓库这个版本下**不给** `localStorage` 全局 —— 宿主的
 * `src/__tests__/setup.ts` 装了内存垫片，所以跑在宿主 vitest 里看不出问题，
 * 包独立 `pnpm test` 才炸。「最小宿主」这份配置得把这条一并扮演掉。
 */
function createMemoryStorage(): Storage {
  const values = new Map<string, string>();

  return {
    get length() {
      return values.size;
    },
    clear() {
      values.clear();
    },
    getItem(key: string) {
      return values.get(key) ?? null;
    },
    key(index: number) {
      return Array.from(values.keys())[index] ?? null;
    },
    removeItem(key: string) {
      values.delete(key);
    },
    setItem(key: string, value: string) {
      values.set(key, value);
    },
  };
}

if (
  typeof globalThis.localStorage === "undefined" ||
  typeof globalThis.localStorage.getItem !== "function"
) {
  const storage = createMemoryStorage();

  Object.defineProperty(globalThis, "localStorage", {
    configurable: true,
    value: storage,
  });
  Object.defineProperty(window, "localStorage", {
    configurable: true,
    value: storage,
  });
}

/**
 * 包内组件（CodeBlock / ThinkingBlock / RichLink…）经 `useUiTranslation()` 取文案，
 * 而实例按设计由**宿主**持有：跑在宿主 vitest 里时 `src/__tests__/setup.ts` 已经
 * import 过 `@/i18n`，默认实例是初始化好的。包独立 `pnpm test` 没有宿主，
 * 所以这里扮演最小宿主：只 init 默认实例并挂上包自己的 namespace。
 *
 * 语言固定 en，与宿主 setup 的 `changeLanguage("en")` 对齐——用例断言的是英文文案。
 */
void i18n.use(initReactI18next).init({
  defaultNS: "common",
  fallbackLng: "en",
  interpolation: { escapeValue: false },
  lng: "en",
  resources: {
    en: { [AGENTRE_UI_NAMESPACE]: agentreUiResources.en },
  },
  react: { useSuspense: false },
});
