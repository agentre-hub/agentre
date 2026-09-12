import { existsSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

import { toneClass, toneClassNames } from "../tones";
import { ISSUE_TONES } from "../types";

/**
 * 色板从五档语义名（`bug` / `critical` / …）改成 8 档颜色名之后，这里钉三件事：
 * **取值域**、**每一档具体落在哪对 token 上**、以及**那些 token 真的存在**。
 */

/** 与 boundary.test.ts / i18n.test.tsx 同一套定位法：宿主与包两个 cwd 都要能跑。 */
function locatePackageRoot(): string {
  for (const candidate of [
    resolve(process.cwd(), "packages/agentre-ui"),
    resolve(process.cwd()),
  ]) {
    const manifestPath = join(candidate, "package.json");
    if (!existsSync(manifestPath)) continue;
    const manifest = JSON.parse(readFileSync(manifestPath, "utf8")) as {
      name?: string;
    };
    if (manifest.name === "@agentre-hub/agentre-ui") return candidate;
  }
  throw new Error("@agentre-hub/agentre-ui package root not found");
}

/** `@theme inline` 里 `--color-x` 暴露出来的那批名字，就是能写成类名的全集。 */
function declaredColorUtilities(): Set<string> {
  const css = readFileSync(
    join(locatePackageRoot(), "styles/tokens.css"),
    "utf8",
  );
  return new Set(
    Array.from(css.matchAll(/--color-([a-z0-9-]+):/g), (match) => match[1]),
  );
}

describe("8 档色调", () => {
  it("Given the tone table, When it is enumerated, Then it is exactly the eight names the wire and the entity use", () => {
    // 逐字钉死：五档旧值经迁移 1:1 变成 red / red_solid / gray / green / steel
    // （规格「数据与迁移」），换掉其中任何一个名字都会让 issue_entity 的
    // allowedTones 与这里对不上，而那种失配只在运行时才看得见。
    expect([...ISSUE_TONES]).toEqual([
      "gray",
      "red",
      "red_solid",
      "amber",
      "green",
      "steel",
      "blue",
      "violet",
    ]);
    expect(Object.keys(toneClassNames).sort()).toEqual([...ISSUE_TONES].sort());
  });

  it("Given each tone, When its classes are read, Then they are the token pair the spec assigns", () => {
    expect(toneClassNames).toEqual({
      gray: "border border-border-strong text-muted-foreground",
      red: "bg-destructive-soft text-destructive-text",
      red_solid:
        "bg-destructive text-destructive-foreground dark:bg-destructive/60",
      amber: "bg-status-waiting-bg text-status-waiting-text",
      green: "bg-status-running-bg text-status-running-text",
      steel: "bg-primary-soft text-primary-text",
      blue: "bg-tone-blue-bg text-tone-blue-text",
      violet: "bg-tone-violet-bg text-tone-violet-text",
    });
  });

  it("Given the tone table, When every colour class is checked against tokens.css, Then none names a token that does not exist", () => {
    // 「本轮不新增任何颜色 token」的机械化：写一个 --tone-red-bg 这类没定义的名字，
    // Tailwind 只是解不出类，标签在真实窗口里悄悄变成没有底色。
    const declared = declaredColorUtilities();
    const missing = Object.entries(toneClassNames).flatMap(([tone, classes]) =>
      classes
        .split(/\s+/)
        .map((token) => token.replace(/^dark:/, "").replace(/\/\d+$/, ""))
        .flatMap((token) => {
          const name = /^(?:bg|text|border)-(.+)$/.exec(token)?.[1];
          if (!name) return [];
          return declared.has(name) ? [] : [`${tone}: ${token}`];
        }),
    );

    expect(missing).toEqual([]);
  });

  it("Given the neutral tone, When it renders, Then it is outlined rather than filled", () => {
    // 暗色下 --secondary 与 --popover 是同一个字节：填充的中性档在任何弹层里
    // 只剩文字。描边不依赖表面色。
    expect(toneClassNames.gray).toContain("border-border-strong");
    expect(toneClassNames.gray).toContain("text-muted-foreground");
    expect(toneClassNames.gray).not.toContain("bg-");
  });

  it("Given an unknown tone from the wire, When it is resolved, Then it falls back to the outlined neutral, never to bg-secondary", () => {
    expect(toneClass("chartreuse")).toBe(toneClassNames.gray);
    expect(toneClass(undefined)).toBe(toneClassNames.gray);
    expect(toneClass("chartreuse")).not.toContain("bg-secondary");
  });

  it("Given a caller that overrides a tone, When it passes extra classes, Then they win the merge", () => {
    expect(toneClass("blue", "bg-card")).toContain("bg-card");
    expect(toneClass("blue", "bg-card")).not.toContain("bg-tone-blue-bg");
  });
});
