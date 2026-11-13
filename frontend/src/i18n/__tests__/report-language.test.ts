import { afterEach, describe, expect, it, vi } from "vitest";

import i18n, { reportLanguageToBackend } from "@/i18n";

type WailsWindow = { go?: { app?: { App?: Record<string, unknown> } } };

function installBinding(setUILanguage: unknown) {
  (window as unknown as WailsWindow).go = {
    app: { App: { SetUILanguage: setUILanguage } },
  };
}

// 后端要知道界面语言：注入会话的记忆段落里的固定文字跟随它（spec 2026-11-11 决策 16）。
describe("界面语言上报给后端", () => {
  afterEach(() => {
    delete (window as unknown as WailsWindow).go;
  });

  it("Given the Wails host, When the user switches the language, Then the new language is reported", async () => {
    const setUILanguage = vi.fn(() => Promise.resolve());
    installBinding(setUILanguage);

    await i18n.changeLanguage("zh-CN");

    expect(setUILanguage).toHaveBeenLastCalledWith("zh-CN");
  });

  it("Given the Wails host, When the current language is reported at startup, Then the supported form is sent", () => {
    const setUILanguage = vi.fn(() => Promise.resolve());
    installBinding(setUILanguage);

    reportLanguageToBackend("en");

    expect(setUILanguage).toHaveBeenCalledWith("en");
  });

  it("Given an unsupported language value, When reporting, Then nothing is sent", () => {
    const setUILanguage = vi.fn(() => Promise.resolve());
    installBinding(setUILanguage);

    reportLanguageToBackend("fr");

    expect(setUILanguage).not.toHaveBeenCalled();
  });

  it("Given no Wails host (tests, browser preview), When the language changes, Then nothing throws", async () => {
    await expect(i18n.changeLanguage("zh-CN")).resolves.toBeDefined();
    expect(() => reportLanguageToBackend("en")).not.toThrow();
  });

  it("Given the binding rejects, When reporting, Then the failure is swallowed and the UI keeps working", async () => {
    const setUILanguage = vi.fn(() => Promise.reject(new Error("db locked")));
    installBinding(setUILanguage);

    expect(() => reportLanguageToBackend("en")).not.toThrow();
    await Promise.resolve();
    expect(setUILanguage).toHaveBeenCalledWith("en");
  });
});
