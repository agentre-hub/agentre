import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

const packageRoot = path.resolve(
  path.dirname(fileURLToPath(import.meta.url)),
  "..",
);
const frontendRoot = path.resolve(packageRoot, "../..");
const repositoryRoot = path.resolve(frontendRoot, "..");

function source(pathname: string): string {
  return readFileSync(pathname, "utf8");
}

describe("Agentre brand artwork", () => {
  it("Given a small in-app surface, when it renders the Agentre mark, then the shared SVG is a bare mark without the app-icon tile", () => {
    const sharedLogo = source(
      path.join(packageRoot, "src/engine/assets/images/logo-mark.svg"),
    );

    // 顶栏 22px、favicon 16px、引擎面板同排的品牌标都吃这一份：瓦片底和滤镜在这个
    // 尺寸下会糊成一块深色方块，所以这里只能是透明底的笔画加两个节点。
    expect(sharedLogo).not.toMatch(/<rect\b/);
    expect(sharedLogo).not.toMatch(/<filter\b/);
    expect(
      source(path.join(packageRoot, "src/engine/ai-brand-logo.tsx")),
    ).toContain('from "./assets/images/logo-mark.svg"');
    expect(
      source(path.join(frontendRoot, "src/components/agentre/chrome.tsx")),
    ).toContain("agentreLogoUrl");
    expect(source(path.join(frontendRoot, "index.html"))).toContain(
      'type="image/svg+xml" href="/packages/agentre-ui/src/engine/assets/images/logo-mark.svg"',
    );
  });

  it("Given native package formats, when the desktop is built, then the tile has its own source and every derived icon exists", () => {
    const appIconSource = source(
      path.join(repositoryRoot, "build/appicon.svg"),
    );
    const appIcon = readFileSync(
      path.join(repositoryRoot, "build/appicon.png"),
    );
    const windowsIcon = readFileSync(
      path.join(repositoryRoot, "build/windows/icon.ico"),
    );

    expect(appIconSource).toMatch(/<rect\b/);
    expect(appIcon.subarray(1, 4).toString("ascii")).toBe("PNG");
    expect(appIcon.readUInt32BE(16)).toBe(1024);
    expect(appIcon.readUInt32BE(20)).toBe(1024);
    expect(windowsIcon.readUInt16LE(0)).toBe(0);
    expect(windowsIcon.readUInt16LE(2)).toBe(1);
    expect(windowsIcon.readUInt16LE(4)).toBe(7);
  });
});
