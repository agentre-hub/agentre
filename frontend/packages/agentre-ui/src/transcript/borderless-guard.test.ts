import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// 转录里的卡片无外框、无内部分隔线(无边框改版)。这里扫源码里画边的类名:
// 除表中的保留项(状态色条、居中分隔标记的两道细线、markdown hr)外一律不许出现。
const ROOT = __dirname;
const EDGE =
  /(?<![\w-])(border(-[tblrxy])?(-\d+)?|divide-[xy]|border-(?!collapse|separate|spacing)[\w/-]+)(?![\w-])/g;

// 允许的行(文件相对路径 -> 该文件里允许出现的整段类名片段)。
const KEPT: Record<string, RegExp[]> = {
  // 子代理运行组 / 结果体的状态色条:只有左侧一条。
  "canonical-tool/agent-spawn/card.tsx": [
    /border-l-2 border-l-status-(error|running|waiting)/,
    /rounded-sm border-l-2 px-2\.5 py-2/,
    /status === "error" \? "border-status-error" : "border-primary"/,
  ],
  // markdown 里 agent 自己写的 <hr>。
  "markdown-text.tsx": [/my-3 border-border/],
};

function sourceFiles(dir: string): string[] {
  return fs.readdirSync(dir, { withFileTypes: true }).flatMap((entry) => {
    const full = path.join(dir, entry.name);
    if (entry.isDirectory()) {
      return entry.name === "__testing__" ? [] : sourceFiles(full);
    }
    return /\.tsx$/.test(entry.name) && !/\.test\.tsx$/.test(entry.name)
      ? [full]
      : [];
  });
}

describe("transcript is borderless", () => {
  it("no source draws an outer frame or inner divider outside the kept status bars", () => {
    const offenders: string[] = [];
    for (const file of sourceFiles(ROOT)) {
      const rel = path.relative(ROOT, file);
      const allowed = KEPT[rel] ?? [];
      fs.readFileSync(file, "utf8")
        .split("\n")
        .forEach((line, i) => {
          if (/^\s*(\/\/|\*|\{?\/\*)/.test(line)) return;
          if (/\bimport\b/.test(line)) return;
          const hits = line.match(EDGE);
          if (!hits) return;
          if (allowed.some((re) => re.test(line))) return;
          offenders.push(`${rel}:${i + 1}: ${hits.join(", ")}`);
        });
    }
    expect(offenders).toEqual([]);
  });
});
