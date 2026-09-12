import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useState,
} from "react";
import type { ReactNode } from "react";

/**
 * 主题的单一实现 —— 桌面端与浏览器宿主共用。
 *
 * 两边原本各写一份，三态（`light | dark | system`）、存储键 `agentre.theme`、
 * `matchMedia("(prefers-color-scheme: dark)")` 订阅、往 `<html>` 上刷 class 与
 * `style.colorScheme` 全都逐字相同——`.dark` 变体本来就由本包的 tokens.css 定义，
 * 契约在包里而两份实现在宿主，是这次要消掉的倒挂。
 *
 * 合并时两处取了**超集**：
 *   - `addListener/removeListener` 兜底：桌面端有，浏览器那份没有。老 webview 上
 *     `MediaQueryList` 只有旧接口，去掉它等于让那些机器不再跟随系统切换。
 *   - `document.documentElement.dataset.theme`：桌面端会写，浏览器那份不写。多写
 *     一个属性不改变任何一边的观感，少写会让依赖 `[data-theme]` 的样式失效。
 */

export type AppTheme = "light" | "dark";
export type AppThemePreference = AppTheme | "system";

export const THEME_STORAGE_KEY = "agentre.theme";

export const THEME_PREFERENCE_ORDER: readonly AppThemePreference[] = [
  "system",
  "light",
  "dark",
];

/**
 * 存储端口，可选。
 *
 * 不传就用宿主浏览器的 `localStorage`（取不到 / 被禁用时静默降级为不持久化——
 * 内嵌预览环境会直接抛）；传 `null` 表示这一份主题不落盘。宿主自己的 storage
 * 封装（桌面端的 `getBrowserStorage()`）可以直接当端口传进来。
 */
export type ThemeStoragePort = {
  getItem(key: string): string | null;
  setItem?(key: string, value: string): void;
};

export type ThemeContextValue = {
  /** 用户的选择，可能是 `system`。 */
  themePreference: AppThemePreference;
  /** 实际生效的外观，`system` 已解析成具体值。 */
  effectiveTheme: AppTheme;
  setThemePreference: (themePreference: AppThemePreference) => void;
};

const ThemeContext = createContext<ThemeContextValue | null>(null);

export function isAppTheme(
  value: string | null | undefined,
): value is AppTheme {
  return value === "light" || value === "dark";
}

export function isAppThemePreference(
  value: string | null | undefined,
): value is AppThemePreference {
  return value === "system" || isAppTheme(value);
}

export function nextThemePreference(
  themePreference: AppThemePreference,
): AppThemePreference {
  const currentIndex = THEME_PREFERENCE_ORDER.indexOf(themePreference);
  const nextIndex =
    (currentIndex < 0 ? 0 : currentIndex + 1) % THEME_PREFERENCE_ORDER.length;

  return THEME_PREFERENCE_ORDER[nextIndex];
}

function getBrowserStorage(): ThemeStoragePort | null {
  if (typeof window === "undefined") return null;

  try {
    return window.localStorage;
  } catch {
    return null;
  }
}

function readStoredThemePreference(
  storage: ThemeStoragePort | null,
): AppThemePreference | null {
  if (typeof storage?.getItem !== "function") return null;

  try {
    const value = storage.getItem(THEME_STORAGE_KEY);
    return isAppThemePreference(value) ? value : null;
  } catch {
    return null;
  }
}

function writeStoredThemePreference(
  storage: ThemeStoragePort | null,
  themePreference: AppThemePreference,
) {
  if (typeof storage?.setItem !== "function") return;

  try {
    storage.setItem(THEME_STORAGE_KEY, themePreference);
  } catch {
    // Some embedded previews block localStorage writes.
  }
}

function prefersDarkTheme(): boolean {
  if (
    typeof window === "undefined" ||
    typeof window.matchMedia !== "function"
  ) {
    return false;
  }

  try {
    return window.matchMedia("(prefers-color-scheme: dark)").matches;
  } catch {
    return false;
  }
}

export function getSystemTheme(): AppTheme {
  return prefersDarkTheme() ? "dark" : "light";
}

export function resolveThemePreference(
  themePreference: AppThemePreference,
  systemTheme: AppTheme,
): AppTheme {
  return themePreference === "system" ? systemTheme : themePreference;
}

/**
 * `.dark` 由本包 tokens.css 的 `@custom-variant dark (&:is(.dark *))` 认定，
 * class / `data-theme` / `color-scheme` 三者是一套，改任意一个都要同步。
 */
export function applyDocumentTheme(theme: AppTheme) {
  if (typeof document === "undefined") return;

  document.documentElement.classList.toggle("dark", theme === "dark");
  document.documentElement.dataset.theme = theme;
  document.documentElement.style.colorScheme = theme;
}

export type ThemeProviderProps = {
  children: ReactNode;
  /** 缺省用浏览器 `localStorage`；`null` = 不持久化。 */
  storage?: ThemeStoragePort | null;
};

export function ThemeProvider({ children, storage }: ThemeProviderProps) {
  const resolvedStorage = useMemo(
    () => (storage === undefined ? getBrowserStorage() : storage),
    [storage],
  );
  const [themePreference, setThemePreference] = useState<AppThemePreference>(
    () => readStoredThemePreference(resolvedStorage) ?? "system",
  );
  const [systemTheme, setSystemTheme] = useState<AppTheme>(getSystemTheme);
  const effectiveTheme = resolveThemePreference(themePreference, systemTheme);

  // 刷 class 走 layout effect：晚一帧生效就是用户能看见的一次白闪。
  useLayoutEffect(() => {
    applyDocumentTheme(effectiveTheme);
    writeStoredThemePreference(resolvedStorage, themePreference);
  }, [effectiveTheme, resolvedStorage, themePreference]);

  // 订阅始终挂着，与当前选择无关：用户从 light 切回 system 时不该需要刷新页面。
  useEffect(() => {
    if (
      typeof window === "undefined" ||
      typeof window.matchMedia !== "function"
    ) {
      return;
    }

    let mediaQuery: MediaQueryList;

    try {
      mediaQuery = window.matchMedia("(prefers-color-scheme: dark)");
    } catch {
      return;
    }

    const handleColorSchemeChange = (event?: MediaQueryListEvent) => {
      setSystemTheme((event?.matches ?? mediaQuery.matches) ? "dark" : "light");
    };

    handleColorSchemeChange();

    if (typeof mediaQuery.addEventListener === "function") {
      mediaQuery.addEventListener("change", handleColorSchemeChange);

      return () => {
        mediaQuery.removeEventListener("change", handleColorSchemeChange);
      };
    }

    // 老 webview 只有 addListener/removeListener——去掉它们那些机器就不跟随系统了。
    const legacyMediaQuery = mediaQuery as MediaQueryList & {
      addListener?: (listener: (event: MediaQueryListEvent) => void) => void;
      removeListener?: (listener: (event: MediaQueryListEvent) => void) => void;
    };

    legacyMediaQuery.addListener?.(handleColorSchemeChange);

    return () => {
      legacyMediaQuery.removeListener?.(handleColorSchemeChange);
    };
  }, []);

  const setPreference = useCallback((next: AppThemePreference) => {
    setThemePreference(next);
  }, []);

  const value = useMemo<ThemeContextValue>(
    () => ({
      themePreference,
      effectiveTheme,
      setThemePreference: setPreference,
    }),
    [effectiveTheme, setPreference, themePreference],
  );

  return (
    <ThemeContext.Provider value={value}>{children}</ThemeContext.Provider>
  );
}

export function useTheme(): ThemeContextValue {
  const value = useContext(ThemeContext);
  if (!value) {
    throw new Error("useTheme must be used inside <ThemeProvider>");
  }
  return value;
}
