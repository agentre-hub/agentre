import { existsSync, readFileSync, statSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import { AGENTRE_UI_NAMESPACE, agentreUiResources } from "../i18n";

import {
  isPresetKey,
  presetForKey,
  presetsByGroup,
  searchPresets,
  type PresetGroup,
} from "./preset-registry";

const GROUPS: PresetGroup[] = ["agent", "brand", "project", "team"];
const here = path.dirname(fileURLToPath(import.meta.url));

/** spec「预设清单」里的顺序：key 的后半段。 */
const SPEC: Record<PresetGroup, string[]> = {
  agent: [
    "assistant",
    "architect",
    "reviewer",
    "tester",
    "debugger",
    "frontend",
    "backend",
    "ops",
    "security",
    "writer",
    "analyst",
    "refactor",
    "performance",
    "planner",
    "translator",
    "shell",
  ],
  brand: [
    "claude",
    "anthropic",
    "openai",
    "codex",
    "pi",
    "openclaw",
    "hermes",
    "deepseek",
    "glm",
    "gemini",
    "qwen",
    "kimi",
    "minimax",
    "mistral",
    "xai",
    "meta",
  ],
  project: [
    "terminal",
    "git",
    "web",
    "mobile",
    "server",
    "database",
    "api",
    "package",
    "containers",
    "cloud",
    "chip",
    "ai",
    "docs",
    "dashboard",
    "game",
    "plugin",
  ],
  team: [
    "dev",
    "frontend",
    "backend",
    "qa",
    "design",
    "ops",
    "security",
    "data",
    "product",
    "docs",
    "marketing",
    "support",
    "research",
    "platform",
    "mobile",
    "leadership",
  ],
};

const dict = (lang: "en" | "zh-CN") => {
  const resources = agentreUiResources[lang] as unknown as Record<
    string,
    Record<string, unknown>
  >;
  return (key: string): string => {
    const value = key
      .replace(`${AGENTRE_UI_NAMESPACE}:`, "")
      .split(".")
      .reduce<unknown>(
        (node, part) => (node as Record<string, unknown> | undefined)?.[part],
        resources[AGENTRE_UI_NAMESPACE] ?? resources,
      );
    return typeof value === "string" ? value : key;
  };
};

describe("preset registry", () => {
  it("Given the four groups, When listed, Then each has 16 keys in spec order", () => {
    for (const group of GROUPS) {
      expect(presetsByGroup(group).map((p) => p.key)).toEqual(
        SPEC[group].map((name) => `preset:${group}-${name}`),
      );
    }
  });

  it("Given every key, Then it starts with preset: and fits 32 bytes and is unique", () => {
    const keys = GROUPS.flatMap((g) => presetsByGroup(g).map((p) => p.key));
    expect(keys).toHaveLength(64);
    expect(new Set(keys).size).toBe(64);
    for (const key of keys) {
      expect(key.startsWith("preset:")).toBe(true);
      expect(Buffer.byteLength(key)).toBeLessThanOrEqual(32);
      expect(isPresetKey(key)).toBe(true);
    }
  });

  it("Given every entry, Then its webp asset exists and the total stays within 320KB", () => {
    let total = 0;
    for (const group of GROUPS) {
      for (const preset of presetsByGroup(group)) {
        const file = path.join(here, "assets", `${preset.key.slice(7)}.webp`);
        expect(existsSync(file), file).toBe(true);
        expect(readFileSync(file).subarray(8, 12).toString()).toBe("WEBP");
        expect(preset.src).toBeTruthy();
        total += statSync(file).size;
      }
    }
    expect(total).toBeLessThanOrEqual(320 * 1024);
  });

  it("Given a preset key, When resolved, Then it carries src, group and a labelKey with zh and en names", () => {
    const hit = presetForKey("preset:project-git");
    expect(hit).toMatchObject({
      key: "preset:project-git",
      group: "project",
    });
    expect(hit?.src).toBeTruthy();
    for (const group of GROUPS) {
      for (const preset of presetsByGroup(group)) {
        for (const lang of ["en", "zh-CN"] as const) {
          const label = dict(lang)(preset.labelKey);
          expect(label, `${preset.key} ${lang}`).not.toBe(preset.labelKey);
          expect(label.trim()).not.toBe("");
        }
      }
    }
    expect(
      dict("zh-CN")(presetForKey("preset:agent-architect")!.labelKey),
    ).toBe("架构师");
    expect(dict("en")(presetForKey("preset:brand-xai")!.labelKey)).toBe("xAI");
  });

  it("Given an unknown preset or a non-preset key, Then presetForKey returns undefined", () => {
    expect(presetForKey("preset:x")).toBeUndefined();
    expect(presetForKey("preset:agent-nobody")).toBeUndefined();
    expect(presetForKey("hammer")).toBeUndefined();
    expect(presetForKey("")).toBeUndefined();
    expect(isPresetKey("preset:x")).toBe(false);
    expect(isPresetKey("hammer")).toBe(false);
  });

  it("Given a query, When searched, Then it matches the localized name, the key tail and honours groups", () => {
    const zh = dict("zh-CN");
    const en = dict("en");
    expect(searchPresets("架构", zh).map((p) => p.key)).toEqual([
      "preset:agent-architect",
    ]);
    expect(searchPresets("架构", en)).toEqual([]);
    expect(searchPresets("GIT", en).map((p) => p.key)).toContain(
      "preset:project-git",
    );
    expect(searchPresets("", en)).toHaveLength(64);
    expect(searchPresets("  ", en)).toHaveLength(64);
    expect(searchPresets("zzzz-no-such", en)).toEqual([]);
    const onlyBrand = searchPresets("", en, ["brand"]);
    expect(onlyBrand).toHaveLength(16);
    expect(onlyBrand.every((p) => p.group === "brand")).toBe(true);
  });
});
