import { readdirSync, readFileSync, statSync } from "node:fs";
import { join, resolve } from "node:path";
import { describe, expect, it } from "vitest";

// 外壳扫荡的守卫：外壳、弹层、面板与共享包的会话索引 / 项目 / 导入 / 引导 / 状态条
// 不再用边或分隔线分区（规格「结构线」）。保留项靠类名区分、不在此列：
// 状态色条（border-l-2 border-status-*）、拖放落点、键盘焦点环。

const ROOT = resolve(__dirname, "../../../..");

const FILES = [
  "src/App.tsx",
  "src/components/agentre/chrome.tsx",
  "src/components/agentre/update-panel.tsx",
  "src/components/agentre/sessions-popover.tsx",
  "src/components/agentre/notification-toast.tsx",
  "src/components/agentre/primitives.tsx",
  "src/components/agentre/under-construction-page.tsx",
  "src/components/agentre/quit-confirm-dialog.tsx",
  "src/components/agentre/project-merge-dialog.tsx",
  "src/components/agentre/project-new-dialog.tsx",
  "src/components/agentre/project-settings-drawer.tsx",
  "src/components/agentre/delete-project-dialog.tsx",
];

const DIRS = [
  "src/components/agentre/session-index",
  "src/components/agentre/command-palette",
  "src/components/agentre/peer",
  "packages/agentre-ui/src/session-index",
  "packages/agentre-ui/src/project",
  "packages/agentre-ui/src/session-import",
  "packages/agentre-ui/src/onboarding",
  "packages/agentre-ui/src/session-status",
];

function walk(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const full = join(dir, name);
    if (statSync(full).isDirectory()) return walk(full);
    return full.endsWith(".tsx") && !/\.test\.tsx$/.test(full) ? [full] : [];
  });
}

const swept = [
  ...FILES.map((f) => join(ROOT, f)),
  ...DIRS.flatMap((d) => walk(join(ROOT, d))),
].filter((f) => !f.endsWith("sources/navigation-source.tsx"));

// 裸 border / border-{t,r,b,l,x,y} / divide-* / ring-1，任意变体前缀；
// 不含带宽度的 border-l-2 状态色条与 border-transparent。
const STRUCTURAL =
  /(?:^|[\s"'`:[])(?:border|border-[trblxy]|divide-[xy]|ring-1)(?=$|[\s"'`\]])/;
const EDGE_COLOUR =
  /(?:^|[\s"'`:])border-(?:border|border-strong|input|ring|sidebar-border)(?=$|[\s"'`/])/;

describe("外壳扫荡后的源码不再画结构线", () => {
  it("覆盖到足够多的文件（防止路径写错导致空转）", () => {
    expect(swept.length).toBeGreaterThan(60);
  });

  it.each(swept.map((f) => [f.slice(ROOT.length + 1), f] as const))(
    "%s",
    (_name, file) => {
      const lines = readFileSync(file, "utf8").split("\n");
      const hits = lines
        .map((line, i) => ({ line, n: i + 1 }))
        .filter(({ line }) => STRUCTURAL.test(line) || EDGE_COLOUR.test(line))
        .map(({ line, n }) => `${n}: ${line.trim().slice(0, 120)}`);
      expect(hits).toEqual([]);
    },
  );
});
