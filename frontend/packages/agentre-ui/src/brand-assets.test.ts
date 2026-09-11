import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { inflateSync } from "node:zlib";

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

// 只解 rsvg-convert 导出的 8-bit RGBA 非隔行 PNG，够读 alpha 通道即可。
function pngAlpha(png: Buffer): (x: number, y: number) => number {
  expect([png[24], png[25], png[28]]).toEqual([8, 6, 0]);
  const width = png.readUInt32BE(16);
  const idat: Buffer[] = [];
  for (let offset = 8; offset < png.length; ) {
    const length = png.readUInt32BE(offset);
    if (png.toString("ascii", offset + 4, offset + 8) === "IDAT") {
      idat.push(png.subarray(offset + 8, offset + 8 + length));
    }
    offset += length + 12;
  }
  const raw = inflateSync(Buffer.concat(idat));
  const bpp = 4;
  const stride = width * bpp;
  const rows = raw.length / (stride + 1);
  const pixels = Buffer.alloc(rows * stride);
  for (let y = 0; y < rows; y++) {
    const filter = raw[y * (stride + 1)];
    const line = y * (stride + 1) + 1;
    const out = y * stride;
    for (let i = 0; i < stride; i++) {
      const left = i >= bpp ? pixels[out + i - bpp] : 0;
      const up = y > 0 ? pixels[out - stride + i] : 0;
      const upLeft = y > 0 && i >= bpp ? pixels[out - stride + i - bpp] : 0;
      let predictor = 0;
      if (filter === 1) predictor = left;
      else if (filter === 2) predictor = up;
      else if (filter === 3) predictor = (left + up) >> 1;
      else if (filter === 4) {
        const p = left + up - upLeft;
        const pa = Math.abs(p - left);
        const pb = Math.abs(p - up);
        const pc = Math.abs(p - upLeft);
        predictor = pa <= pb && pa <= pc ? left : pb <= pc ? up : upLeft;
      }
      pixels[out + i] = (raw[line + i] + predictor) & 0xff;
    }
  }
  return (x, y) => pixels[(y * width + x) * bpp + 3];
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

  it("Given the macOS Dock, when it lays out app icons, then the exported tile sits on Apple's 824px grid instead of bleeding to the canvas edge", () => {
    const alpha = pngAlpha(
      readFileSync(path.join(repositoryRoot, "build/appicon.png")),
    );

    // 系统图标在 1024 画布上是 100→924 的底板，四周留 100px 给投影；满版的底板在
    // Dock 里会比旁边的图标大一圈。
    expect(alpha(512, 512)).toBe(255);
    expect(alpha(512, 106)).toBe(255);
    expect(alpha(106, 512)).toBe(255);
    expect(alpha(918, 512)).toBe(255);
    expect(alpha(512, 80)).toBeLessThan(16);
    expect(alpha(80, 512)).toBeLessThan(64);
    expect(alpha(944, 512)).toBeLessThan(64);
    expect(alpha(512, 940)).toBeLessThan(128);
  });

  it("Given the Windows taskbar, when icon.ico is re-exported, then it comes from its own full-bleed source rather than the padded macOS tile", () => {
    const windowsIconSource = source(
      path.join(repositoryRoot, "build/windows/icon.svg"),
    );

    // Windows 没有统一底板规范，任务栏 24px 下再留 macOS 的边距只会显小。
    expect(windowsIconSource).toMatch(
      /<rect x="64" y="64" width="896" height="896"/,
    );
    expect(source(path.join(repositoryRoot, "build/appicon.svg"))).not.toMatch(
      /width="896"/,
    );
  });
});
