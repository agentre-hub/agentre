import fs from "node:fs";
import path from "node:path";

import { describe, expect, it } from "vitest";

/**
 * 输入框那一带的「一份实现」守卫。
 *
 * 上下文计量器、token 格式化与转录行间距类名,此前桌面端与 agentre-server 各持
 * 一份**逐行同构**的副本 —— 环几何、200/100 悬停延迟、色阶表、228px 面板、两种
 * 语言的文案全都一样,靠的是有人记得两边一起改。它们已经收进共享包。
 *
 * 单测拦不住这种回流:任何人重新在 chat.tsx 里写一个 ContextMeter,现有断言照样
 * 全绿(它们只看渲染结果)。所以这里扫源码 —— 与 destructive-fill.test.ts 是同一
 * 种守卫,把「只有一个地方可改」变成机器判得出的东西。
 */
const CHAT_TSX = path.resolve(__dirname, "../chat.tsx");
const CHAT_PANEL_TSX = path.resolve(__dirname, "../chat-panel.tsx");
const PACKAGE = "@agentre-ai/agentre-ui";

function chatSource(): string {
  return fs.readFileSync(CHAT_TSX, "utf8");
}

/** 从一份源码里收集所有自 `@agentre-ai/agentre-ui` 导入的名字。 */
function importedFromPackage(source = chatSource()): Set<string> {
  const names = new Set<string>();
  const pattern = new RegExp(
    `import\\s+(?:type\\s+)?\\{([^}]*)\\}\\s+from\\s+"${PACKAGE.replace("/", "\\/")}"`,
    "g",
  );
  for (const match of source.matchAll(pattern)) {
    for (const raw of match[1].split(",")) {
      const name = raw
        .trim()
        .replace(/^type\s+/, "")
        .split(/\s+as\s+/)[0];
      if (name) names.add(name);
    }
  }
  return names;
}

describe("composer single source", () => {
  it("Given chat.tsx, When it needs the context meter, Then it imports the shared one instead of defining its own", () => {
    const source = chatSource();

    expect(source).not.toMatch(/function\s+ContextMeter\s*\(/);
    expect(source).not.toMatch(/function\s+ContextRing\s*\(/);
    expect(source).not.toMatch(/function\s+ContextPanel\s*\(/);
    // 计量器由 chat-panel 摆进底栏的 trailingControls —— composer 不再收
    // contextUsage,槽位里放什么是宿主的事。
    const panel = fs.readFileSync(CHAT_PANEL_TSX, "utf8");
    expect(importedFromPackage(panel)).toContain("ContextMeter");
    expect(panel).not.toMatch(/function\s+ContextMeter\s*\(/);
  });

  it("Given chat.tsx, When a token count needs formatting, Then no local formatter is defined here", () => {
    // 桌面端此前有一份与 agentre-server 的 lib/sessionView.ts **逐字节相同**的
    // formatTokens。它随计量器一起搬进了包，chat.tsx 现在一处也用不到它 ——
    // 这里守的是「不许再长回来」，而不是「必须 import」（未用的 import 过不了 lint）。
    expect(chatSource()).not.toMatch(/function\s+formatTokens\s*\(/);
  });

  it("Given chat.tsx, When it levels quota usage, Then the 90/75 thresholds come from the package", () => {
    const source = chatSource();

    // 阈值只有一份:上下文计量器搬进包之后,配额计量器不许在这里另写一套 —— 这
    // 正是原 quotaLevel 注释警告过的形态。
    expect(importedFromPackage()).toContain("usageLevel");
    expect(source).not.toMatch(/percent\s*>=\s*90/);
    expect(source).not.toMatch(/percent\s*>=\s*75/);
  });

  it("Given chat.tsx, When it spaces transcript rows, Then the class names come from the package", () => {
    const source = chatSource();

    // 类名与包里的 px 常量是同一件事;各写一份就会「估高动了、渲染没动」。
    expect(source).not.toMatch(/rowWrapperPad/);
    expect(importedFromPackage()).toContain("transcriptRowPadClass");
  });

  it("Given chat.tsx, When it renders the composer, Then it only wires host capabilities onto the shared one", () => {
    const source = chatSource();

    // 这里曾有一个 504 行的同名组件:自持编辑模式、命令模式、图片附件、拖入与
    // 整条底栏。它现在是装配根 —— 只把这一端独有的能力(提及数据源、技能命令、
    // Wails 拖入通道)接上去,一行 UI 都不画。
    expect(importedFromPackage()).toContain("ChatComposer");
    expect(source).not.toMatch(/<form\b/);
    expect(source).not.toMatch(/chatComposer\.editing/);
    expect(source).not.toMatch(/chatComposer\.command/);
    expect(source).not.toMatch(/MAX_CHAT_IMAGE_COUNT/);
  });
});
