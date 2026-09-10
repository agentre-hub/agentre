import { act, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { createInstance } from "i18next";
import type { i18n as I18nInstance } from "i18next";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { I18nextProvider, initReactI18next } from "react-i18next";

import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "../i18n";

import {
  THEME_STORAGE_KEY,
  ThemeProvider,
  ThemeToggle,
  nextThemePreference,
  useTheme,
} from "./index";
import type { AppThemePreference, ThemeStoragePort } from "./index";

/**
 * 判据来自搬迁前**桌面端 App.tsx 那份实现**的行为：存储键 `agentre.theme`、
 * 三态循环 `system → light → dark`、`<html>` 上同时刷 class / `data-theme` /
 * `color-scheme`、`matchMedia` 订阅带 `addListener` 老接口兜底。
 * 浏览器宿主那份是它的子集（无老接口兜底、不写 `data-theme`），合并取超集。
 */

type MediaListener = (event: { matches: boolean }) => void;

type FakeMediaQuery = {
  matches: boolean;
  addEventListener?: (type: string, listener: MediaListener) => void;
  removeEventListener?: (type: string, listener: MediaListener) => void;
  addListener?: (listener: MediaListener) => void;
  removeListener?: (listener: MediaListener) => void;
  emit(matches: boolean): void;
  listenerCount(): number;
};

function installMatchMedia(options: { legacyOnly?: boolean } = {}) {
  const listeners = new Set<MediaListener>();
  const query: FakeMediaQuery = {
    matches: false,
    emit(matches: boolean) {
      query.matches = matches;
      for (const listener of [...listeners]) listener({ matches });
    },
    listenerCount: () => listeners.size,
  };

  if (options.legacyOnly) {
    query.addListener = (listener) => listeners.add(listener);
    query.removeListener = (listener) => listeners.delete(listener);
  } else {
    query.addEventListener = (_type, listener) => listeners.add(listener);
    query.removeEventListener = (_type, listener) => listeners.delete(listener);
  }

  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    writable: true,
    value: () => query as unknown as MediaQueryList,
  });

  return query;
}

function memoryStorage(initial?: string): ThemeStoragePort & {
  writes: string[];
} {
  let value = initial ?? null;
  const writes: string[] = [];
  return {
    writes,
    getItem: (key: string) => (key === THEME_STORAGE_KEY ? value : null),
    setItem: (key: string, next: string) => {
      if (key !== THEME_STORAGE_KEY) return;
      value = next;
      writes.push(next);
    },
  };
}

let i18n: I18nInstance;

beforeEach(async () => {
  i18n = createInstance();
  await i18n.use(initReactI18next).init({
    lng: "zh-CN",
    fallbackLng: "zh-CN",
    ns: [AGENTRE_UI_NAMESPACE],
    defaultNS: AGENTRE_UI_NAMESPACE,
    resources: {
      "zh-CN": { [AGENTRE_UI_NAMESPACE]: agentreUiResources["zh-CN"] },
      en: { [AGENTRE_UI_NAMESPACE]: agentreUiResources.en },
    },
    interpolation: { escapeValue: false },
  });
});

afterEach(() => {
  document.documentElement.className = "";
  document.documentElement.removeAttribute("data-theme");
  document.documentElement.style.colorScheme = "";
});

function ThemeProbe() {
  const { effectiveTheme, themePreference } = useTheme();
  return (
    <span data-testid="probe">{`${themePreference}/${effectiveTheme}`}</span>
  );
}

function renderThemed(
  storage?: ThemeStoragePort | null,
  children = (
    <>
      <ThemeProbe />
      <ThemeToggle className="mt-auto size-10" />
    </>
  ),
) {
  return render(
    <I18nextProvider i18n={i18n}>
      <ThemeProvider storage={storage}>{children}</ThemeProvider>
    </I18nextProvider>,
  );
}

describe("nextThemePreference", () => {
  it("Given 任一档, When 取下一档, Then 按 system → light → dark 循环", () => {
    const seen: AppThemePreference[] = [];
    let current: AppThemePreference = "system";
    for (let step = 0; step < 4; step++) {
      current = nextThemePreference(current);
      seen.push(current);
    }

    expect(seen).toEqual(["light", "dark", "system", "light"]);
  });

  // 桌面端原实现：`indexOf` 给 -1 时把下标钳成 0，也就是回到表头的 system。
  it("Given 不在表里的脏值, When 取下一档, Then 回到表头 system 而不是抛错", () => {
    expect(nextThemePreference("sepia" as AppThemePreference)).toBe("system");
  });
});

describe("ThemeProvider", () => {
  it("Given 存储里存着 dark, When 首次渲染, Then 生效外观是 dark 且 <html> 三处同时刷上", () => {
    installMatchMedia();
    renderThemed(memoryStorage("dark"));

    expect(screen.getByTestId("probe")).toHaveTextContent("dark/dark");
    expect(document.documentElement).toHaveClass("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
    expect(document.documentElement.style.colorScheme).toBe("dark");
  });

  it("Given 存储里是不认得的值, When 首次渲染, Then 退回 system", () => {
    installMatchMedia();
    renderThemed(memoryStorage("sepia"));

    expect(screen.getByTestId("probe")).toHaveTextContent("system/light");
  });

  it("Given 选择是 system, When 系统切到深色, Then 生效外观跟着变", async () => {
    const media = installMatchMedia();
    renderThemed(memoryStorage("system"));

    expect(screen.getByTestId("probe")).toHaveTextContent("system/light");

    await act(() => media.emit(true));

    expect(screen.getByTestId("probe")).toHaveTextContent("system/dark");
    expect(document.documentElement).toHaveClass("dark");
  });

  it("Given 选择是 light, When 系统切到深色, Then 外观不变但订阅仍挂着", async () => {
    const media = installMatchMedia();
    renderThemed(memoryStorage("light"));

    await act(() => media.emit(true));

    expect(screen.getByTestId("probe")).toHaveTextContent("light/light");
    expect(media.listenerCount()).toBe(1);
  });

  it("Given 只有 addListener 的老 webview, When 系统切到深色, Then 一样跟随（桌面端的兜底不能丢）", async () => {
    const media = installMatchMedia({ legacyOnly: true });
    renderThemed(memoryStorage("system"));

    expect(media.listenerCount()).toBe(1);

    await act(() => media.emit(true));

    expect(screen.getByTestId("probe")).toHaveTextContent("system/dark");
  });

  it("Given 组件卸载, When 检查订阅, Then 老接口那条也退订了", () => {
    const media = installMatchMedia({ legacyOnly: true });
    const view = renderThemed(memoryStorage("system"));

    view.unmount();

    expect(media.listenerCount()).toBe(0);
  });

  it("Given 完全没有 matchMedia 的环境, When 渲染, Then 按浅色跑而不是抛错", () => {
    Object.defineProperty(window, "matchMedia", {
      configurable: true,
      writable: true,
      value: undefined,
    });

    renderThemed(memoryStorage());

    expect(screen.getByTestId("probe")).toHaveTextContent("system/light");
  });

  it("Given storage 传 null, When 切换主题, Then 不落盘也不抛错", async () => {
    installMatchMedia();
    const user = userEvent.setup();
    renderThemed(null);

    await user.click(screen.getByRole("button"));

    expect(screen.getByTestId("probe")).toHaveTextContent("light/light");
  });

  it("Given 在 provider 之外调 useTheme, When 渲染, Then 抛错而不是给一份默认值", () => {
    expect(() => render(<ThemeProbe />)).toThrow(/ThemeProvider/);
  });
});

describe("ThemeToggle", () => {
  it("Given 依次点击, When 走三下, Then 按 system → light → dark → system 循环并逐次落盘", async () => {
    installMatchMedia();
    const storage = memoryStorage("system");
    const user = userEvent.setup();
    renderThemed(storage);

    const toggle = screen.getByRole("button");
    await user.click(toggle);
    expect(screen.getByTestId("probe")).toHaveTextContent("light/light");

    await user.click(toggle);
    expect(screen.getByTestId("probe")).toHaveTextContent("dark/dark");

    await user.click(toggle);
    expect(screen.getByTestId("probe")).toHaveTextContent("system/light");

    expect(storage.writes).toEqual(["system", "light", "dark", "system"]);
  });

  it("Given 选的是 system, When 读无障碍文案, Then 同时念出解析后的外观与下一档", () => {
    installMatchMedia();
    renderThemed(memoryStorage("system"));

    const toggle = screen.getByRole("button");
    const current = i18n.t("theme.systemWithResolved", {
      resolved: i18n.t("theme.light"),
    });

    expect(toggle).toHaveAttribute(
      "aria-label",
      i18n.t("theme.toggle", {
        current,
        next: i18n.t("theme.lightMode"),
      }),
    );
    expect(toggle).toHaveAttribute(
      "title",
      i18n.t("theme.toggleTitle", {
        current,
        next: i18n.t("theme.lightMode"),
      }),
    );
  });

  it("Given 选的是具体外观, When 读无障碍文案, Then 念的是那一档的名字", () => {
    installMatchMedia();
    renderThemed(memoryStorage("dark"));

    expect(screen.getByRole("button")).toHaveAttribute(
      "aria-label",
      i18n.t("theme.toggle", {
        current: i18n.t("theme.darkMode"),
        next: i18n.t("theme.system"),
      }),
    );
  });

  it("Given 宿主传进外壳类名, When 渲染, Then 类名落在按钮上（外壳样式归宿主）", () => {
    installMatchMedia();
    renderThemed(memoryStorage("system"));

    const toggle = screen.getByRole("button");
    expect(toggle).toHaveClass("mt-auto");
    expect(toggle).toHaveClass("size-10");
    expect(toggle).not.toHaveClass("size-9");
  });
});
