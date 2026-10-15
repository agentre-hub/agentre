import "@testing-library/jest-dom/vitest";

import { render, screen } from "@testing-library/react";
import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../../../../wailsjs/go/app/App", () => ({
  GetAppSetting: vi.fn(() => Promise.reject(new Error("nf"))),
  UpdateAppSettings: vi.fn(() => Promise.resolve({})),
  GetCtlSkillStatus: vi.fn(() =>
    Promise.resolve({
      pluginId: "p",
      pluginInstalled: true,
      pluginPath: "/plug-path",
      universalInstalled: false,
      universalPath: "/univ-path",
    }),
  ),
}));

import i18n from "../../../i18n";
import {
  DEFAULT_FILE_SETTINGS,
  useFileSettingsStore,
} from "../../../stores/file-settings-store";
import {
  DEFAULT_NOTIFICATION_SETTINGS,
  useNotificationSettingsStore,
} from "../../../stores/notification-settings-store";
import { CtlSkillPanel } from "../ctl-skill";
import { FileSettingsPanel } from "../file-settings-panel";
import { NotificationsPanel } from "../notifications-panel";

// Structural lines drawn by the settings sub-pages: edge utilities on a box,
// the divider colour, divide-* between siblings. Checkbox/radio/switch edges
// live in shared primitives, not in these sources.
const LINE_CLASS =
  /(^|[\s"'`])(border|border-[btlrxy]|border-[btlrxy]-[^\s"'`]+|border-border|border-border-strong|border-input|divide-[^\s"'`]+|border-dashed|border-solid)(?=$|[\s"'`])/;

function lineClasses(container: HTMLElement): string[] {
  const hits: string[] = [];
  for (const el of container.querySelectorAll("*")) {
    // Checkbox / radio / switch keep their edge by design.
    if (el.closest('[role="switch"],[role="checkbox"],[role="radio"]'))
      continue;
    const cls = el.getAttribute("class") ?? "";
    for (const token of cls.split(/\s+/)) {
      if (LINE_CLASS.test(` ${token}`)) hits.push(token);
    }
  }
  return hits;
}

function filledGroups(container: HTMLElement): Element[] {
  return [...container.querySelectorAll("*")].filter((el) =>
    (el.getAttribute("class") ?? "").split(/\s+/).includes("bg-field-fill"),
  );
}

beforeEach(async () => {
  await i18n.changeLanguage("zh-CN");
  useFileSettingsStore.setState({ settings: { ...DEFAULT_FILE_SETTINGS } });
  useNotificationSettingsStore.setState({
    settings: { ...DEFAULT_NOTIFICATION_SETTINGS },
  });
});
afterEach(() => vi.restoreAllMocks());

describe("settings content groups", () => {
  it("Given the notifications panel, Then groups are filled blocks with no structural lines", () => {
    const { container } = render(<NotificationsPanel />);
    expect(filledGroups(container).length).toBeGreaterThan(0);
    expect(lineClasses(container)).toEqual([]);
  });

  it("Given the file settings panel, Then groups are filled blocks with no structural lines", () => {
    const { container } = render(<FileSettingsPanel />);
    expect(filledGroups(container).length).toBeGreaterThan(0);
    expect(lineClasses(container)).toEqual([]);
  });

  it("Given the ctl skill panel, Then the group is a filled block with no structural lines", async () => {
    const { container } = render(<CtlSkillPanel />);
    await screen.findByText("/plug-path");
    expect(filledGroups(container).length).toBeGreaterThan(0);
    expect(lineClasses(container)).toEqual([]);
  });
});

describe("settings content sources", () => {
  const root = resolve(__dirname, "../../../..");
  const owned = [
    "src/components/agentre/settings.tsx",
    "src/components/agentre/settings-proxy.tsx",
    "src/components/agentre/llm-providers.tsx",
    "src/components/agentre/agent-backends.tsx",
    "src/components/agentre/agent-list.tsx",
    "src/components/agentre/notifications-panel.tsx",
    "src/components/agentre/file-settings-panel.tsx",
    "src/components/agentre/ctl-skill.tsx",
    "src/components/agentre/update-section.tsx",
    "src/components/agentre/remote-devices",
    "src/components/agentre/sync",
    "src/components/agentre/update-section",
    "src/components/agentre/data-backup",
    "src/components/agentre/model-target-picker",
    "src/components/agentre/model-pill",
    "src/components/agentre/reasoning-effort",
    "packages/agentre-ui/src/engine",
    "packages/agentre-ui/src/port-forward",
  ];
  // picker-trigger is a field (task 2); it keeps its own edge handling.
  const skip = (p: string) =>
    /\.test\.tsx?$/.test(p) || p.endsWith("picker-trigger.tsx");

  function walk(p: string): string[] {
    const full = join(root, p);
    if (statSync(full).isFile()) return skip(p) ? [] : [p];
    return readdirSync(full).flatMap((n) => walk(join(p, n)));
  }

  it("draws no structural line (border/divide/border-border) in any settings content source", () => {
    const offenders: string[] = [];
    for (const file of owned.flatMap(walk).filter((f) => f.endsWith(".tsx"))) {
      const lines = readFileSync(join(root, file), "utf8").split("\n");
      lines.forEach((line, i) => {
        if (/^\s*(\/\/|\*|\/\*)/.test(line)) return;
        for (const m of line.matchAll(/[\w:/.[\]-]+/g)) {
          if (LINE_CLASS.test(` ${m[0]}`))
            offenders.push(`${file}:${i + 1} ${m[0]}`);
        }
      });
    }
    expect(offenders).toEqual([]);
  });

  it("leaves no dangling class fragment (a token starting with '-') from removed border classes", () => {
    const offenders: string[] = [];
    for (const file of owned.flatMap(walk).filter((f) => f.endsWith(".tsx"))) {
      readFileSync(join(root, file), "utf8")
        .split("\n")
        .forEach((line, i) => {
          for (const m of line.matchAll(
            /["'`\s](-(?:text|soft|[a-z]+)(?:\/\d+)?)(?=["'`\s])/g,
          )) {
            offenders.push(`${file}:${i + 1} ${m[1]}`);
          }
        });
    }
    expect(offenders).toEqual([]);
  });
});
