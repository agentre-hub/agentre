import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

// 对话流排版护栏。不逐组件断言 class(那种测试与实现同构、无信息量),
// 只禁止若干已被 token 取代的字面量在对话流链路上复活。
//
// chat.tsx 同时装着 transcript 和 composer —— 646/654/714/726 行的圆角与阴影
// 属于输入框,不归对话流卡片系统管。所以规则按文件裁剪,不一刀切。
const AGENTRE_DIR = path.resolve(__dirname, "..");
// 对话流组件正在往共享包 @agentre-ai/agentre-ui 搬。护栏必须跟着被守卫的代码走 ——
// 否则「搬进包」就等于让一个文件悄悄脱离排版护栏。root 指明该文件现在住哪。
const PACKAGE_TRANSCRIPT_DIR = path.resolve(
  __dirname,
  "../../../../packages/agentre-ui/src/transcript",
);

type RuleGroup = "type" | "measure" | "shadow" | "radius";
type SourceRoot = "host" | "package";

const ROOT_DIRS: Record<SourceRoot, string> = {
  host: AGENTRE_DIR,
  package: PACKAGE_TRANSCRIPT_DIR,
};

const RULES: { group: RuleGroup; pattern: RegExp; why: string }[] = [
  { group: "type", pattern: /text-\[9px\]/, why: "低于可读下限,用 text-meta" },
  { group: "type", pattern: /text-\[10px\]/, why: "低于可读下限,用 text-meta" },
  { group: "type", pattern: /text-\[11px\]/, why: "低于可读下限,用 text-meta" },
  { group: "measure", pattern: /max-w-\[720px\]/, why: "用 max-w-measure" },
  { group: "measure", pattern: /max-w-\[580px\]/, why: "用 max-w-measure" },
  { group: "shadow", pattern: /shadow-sm/, why: "对话流卡片不带阴影" },
  { group: "radius", pattern: /rounded-md/, why: "对话流卡片统一 rounded-lg" },
];

// 已知位于对话流链路上、但本轮刻意未纳入护栏的文件及原因(别误当成漏项):
// - chat-input/mentions/transcript.tsx:正被另一个并发会话大改(@ 提及序列化/
//   还原),本轮不碰,等它落地后再评估是否收编。
//
// SCANNED:对话流渲染链路上的组件。新增 transcript 组件时必须加进来 ——
// 让「加进对话流」成为一个需要过目排版护栏的动作。
// skip:该文件豁免的规则组,每一项都要写清理由。
// root:文件所在的源码根,缺省 "host"(src/components/agentre);已搬进共享包的
//      写 "package"(packages/agentre-ui/src/transcript)。
export const SCANNED: {
  file: string;
  skip?: RuleGroup[];
  root?: SourceRoot;
}[] = [
  { file: "transcript-card.tsx", root: "package" },
  // message-row.tsx 的 MESSAGE_AVATAR_CLASS 用 text-[11px] 画 28px 圆形头像里的
  // 姓名首字母字形,不是正文/元信息文字。那是头像专属尺寸,不归 12px text-meta 管。
  { file: "message-row.tsx", skip: ["type"] },
  { file: "markdown-text.tsx", root: "package" },
  { file: "markdown-image.tsx", root: "package" },
  { file: "code-block.tsx", root: "package" },
  { file: "thinking-block.tsx", root: "package" },
  // collapsible-code.tsx 是卡片共用的长内容滚动块。复制角标按钮用 rounded
  // (非 rounded-md)、尺寸用 size-3/size-5(非 text-[9/10/11]px)。
  { file: "collapsible-code.tsx", root: "package" },
  // rich-link.tsx 被 markdown-text.tsx 注册为 markdown 的 `a` 渲染器,每条含链接
  // 的消息都会渲染它,是对话流组件。7 处 rounded-md 分两类:3 处是手写的 Copy
  // <button>,故意与全局 shadcn Button 保持一致的 rounded-md;4 处是 HoverCard
  // 悬浮预览里的内层信息条/详情块,悬浮卡外壳本身是 HoverCardContent(已经
  // rounded-lg),这 4 处不是「卡片外壳」,不受 rounded-lg 约束。故整文件豁免
  // radius 组。
  { file: "rich-link.tsx", skip: ["radius"], root: "package" },
  // 活动块:折叠态组头 + 展开态活动行 + 行内就地展开体。对话流的新形态,
  // 全套字号走 text-meta / text-aux,无卡片外壳(不受 radius/shadow 影响)。
  { file: "activity-block/block.tsx", root: "package" },
  { file: "activity-block/row.tsx", root: "package" },
  { file: "canonical-tool/raw/card.tsx", root: "package" },
  // file.write / file.edit 的卡壳已随聚合改动删除(它们只会折进活动块,
  // 路由永远到不了),留下的是给活动行展开体复用的两个正文渲染器 ——
  // 守卫范围跟着正文走,别把已覆盖的行漏出去。
  { file: "canonical-tool/file-edit/hunk-renderer.tsx", root: "package" },
  { file: "canonical-tool/file-write/content-renderer.tsx", root: "package" },
  { file: "canonical-tool/agent-spawn/card.tsx", root: "package" },
  { file: "canonical-tool/plan/card.tsx", root: "package" },
  { file: "tool-approval/card.tsx" },
  { file: "canonical-tool/tool-permission/card.tsx", root: "package" },
  // user-ask/card.tsx 的选项按钮 / “其他”输入行 / 错误横幅是手写交互控件,
  // 故意与全局 shadcn Button/Input 保持一致的 rounded-md,不跟卡片外壳走
  // rounded-lg —— 否则同一张卡片里手写按钮与底部共享 Button 圆角会打架。
  // 卡片外壳圆角由 TranscriptCard 提供,不是这个文件里的字面量,不受此豁免影响。
  {
    file: "canonical-tool/user-ask/card.tsx",
    skip: ["radius"],
    root: "package",
  },
  { file: "local-command/card.tsx" },
  { file: "transcript-row-view.tsx" },
  // chat.tsx 同时装着 transcript 和 composer —— 646/654/714/726 行的圆角与阴影
  // 属于输入框 / 拖放提示层 / 附件缩略图,不归对话流卡片系统管,故意不跟随
  // rounded-lg / 去阴影;字号与 measure 约束（type/measure 两组）仍然全文件生效。
  { file: "chat.tsx", skip: ["shadow", "radius"] },
  { file: "compact-boundary-divider.tsx", root: "package" },
  { file: "compact-history-fold.tsx" },
  { file: "auto-trigger-banner.tsx" },
];

function violations(source: string, skip: RuleGroup[] = []): string[] {
  return RULES.filter(
    (r) => !skip.includes(r.group) && r.pattern.test(source),
  ).map((r) => `${r.pattern.source} —— ${r.why}`);
}

describe("transcript typography guard", () => {
  it("检测器能抓到违规源码", () => {
    expect(violations('<div className="text-[9px] rounded-md" />')).toEqual([
      "text-\\[9px\\] —— 低于可读下限,用 text-meta",
      "rounded-md —— 对话流卡片统一 rounded-lg",
    ]);
  });

  it("检测器尊重 skip", () => {
    expect(violations('<div className="rounded-md" />', ["radius"])).toEqual(
      [],
    );
  });

  it("对话流组件不含被禁的排版字面量", () => {
    const found = SCANNED.flatMap(({ file, skip, root = "host" }) => {
      const source = fs.readFileSync(path.join(ROOT_DIRS[root], file), "utf8");
      return violations(source, skip).map((v) => `${root}/${file}: ${v}`);
    });
    expect(found).toEqual([]);
  });
});
