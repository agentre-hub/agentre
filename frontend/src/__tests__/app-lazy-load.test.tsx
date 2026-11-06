import { act, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import App from "../App";
import { useCommandPaletteStore } from "@/stores/command-palette-store";
import { useSettingsDialogStore } from "@/stores/settings-dialog-store";

// vi.mock 的工厂只在模块第一次被真正 import 时求值（且只求值一次）：
// 首个用例跑完仍没被求值 = 启动包没加载它。
const evaluated = vi.hoisted(() => new Set<string>());

vi.mock("@/components/agentre/settings/page", () => {
  evaluated.add("settings");
  return { SettingsDialog: () => <div>settings-dialog-stub</div> };
});
vi.mock("@/components/agentre/command-palette/command-palette", () => {
  evaluated.add("palette");
  return { CommandPalette: () => <div>palette-stub</div> };
});
vi.mock("@/components/agentre/hooks-page", () => {
  evaluated.add("hooks");
  return { HooksPage: () => <div>hooks-page-stub</div> };
});
vi.mock("@/components/agentre/issues-page", () => {
  evaluated.add("issues");
  return { IssuesPage: () => <div>issues-page-stub</div> };
});
vi.mock("@/components/agentre/org-chart-page", () => {
  evaluated.add("org");
  return { OrgChartPage: () => <div>org-page-stub</div> };
});

vi.mock("../../wailsjs/runtime/runtime", async () => {
  const actual = await vi.importActual<
    typeof import("../../wailsjs/runtime/runtime")
  >("../../wailsjs/runtime/runtime");
  return { ...actual, EventsOn: vi.fn(() => () => {}), EventsOff: vi.fn() };
});

beforeEach(() => {
  window.localStorage.clear();
  useSettingsDialogStore.setState({ open: false, page: "appearance" });
  useCommandPaletteStore.setState({ open: false, initialQuery: "" });
});

afterEach(() => {
  Reflect.deleteProperty(window, "runtime");
});

describe("App lazy loading", () => {
  it("does not load settings, palette or non-chat pages on the chat route", async () => {
    window.localStorage.setItem("agentre.lastPath", "/chat");
    render(<App />);
    await screen.findByTestId("nav-settings");
    expect([...evaluated]).toEqual([]);
  });

  it("loads the settings dialog on first open", async () => {
    window.localStorage.setItem("agentre.lastPath", "/chat");
    render(<App />);
    await screen.findByTestId("nav-settings");
    act(() => useSettingsDialogStore.getState().openSettings());
    expect(await screen.findByText("settings-dialog-stub")).toBeInTheDocument();
  });

  it("loads the command palette on first open", async () => {
    window.localStorage.setItem("agentre.lastPath", "/chat");
    render(<App />);
    await screen.findByTestId("nav-settings");
    act(() => useCommandPaletteStore.getState().setOpen(true));
    expect(await screen.findByText("palette-stub")).toBeInTheDocument();
  });

  it("loads a non-chat page only when its route is visited", async () => {
    window.localStorage.setItem("agentre.lastPath", "/hooks");
    render(<App />);
    expect(await screen.findByText("hooks-page-stub")).toBeInTheDocument();
  });
});
