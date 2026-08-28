import { FileTypeIcon } from "@/components/agentre/file-type-icon";
import { cn } from "@/lib/utils";

import type { PreviewSourceMode } from "@/stores/file-preview-tabs-store";

import type { GitStatusMeta } from "../git-rows";

import { SidebarRow } from "./sidebar-row";

type Props = {
  sessionId: number;
  cwd: string;
  remote: boolean;
  sourceMode: PreviewSourceMode;
  /** 相对工作根的路径：行的 key，也是预览的入参。 */
  path: string;
  name: string;
  /** 目录后缀；根目录下的文件为空串。 */
  dir: string;
  /** 首列状态符号的字母与配色，两档共用 git-rows 的那一份映射。 */
  meta: GitStatusMeta;
  /** 状态的文字标签：字母对读屏隐藏，语义由它承担。 */
  statusLabel: string;
  plus: number;
  minus: number;
  title?: string;
  /** 仅「本次会话」档：跳到该文件最后被改动的轮次。 */
  onJumpToTurn?: () => void;
  testId: string;
  rowData?: Record<string, string | undefined>;
};

/**
 * ChangeRowView 是「变更」页两档唯一的行渲染实现（spec 决策 12：变更天然跨目录，
 * 两档都是扁平列表、形态统一）。收敛到一处是刻意的：两档此前各写一遍行，状态
 * 字母列、目录后缀的截断方向与 `±N` 角标的取舍就会各自漂移。
 *
 * 行的交互语义仍全部来自 SidebarRow：单击开临时预览标签、双击转常驻、⋯ 与右键
 * 是同一份菜单。
 */
export function ChangeRowView({
  sessionId,
  cwd,
  remote,
  sourceMode,
  path,
  name,
  dir,
  meta,
  statusLabel,
  plus,
  minus,
  title,
  onJumpToTurn,
  testId,
  rowData,
}: Props) {
  return (
    <SidebarRow
      sessionId={sessionId}
      cwd={cwd}
      remote={remote}
      sourceMode={sourceMode}
      kind="file"
      path={path}
      name={name}
      depth={0}
      title={title ?? path}
      onJumpToTurn={onJumpToTurn}
      // 扁平行的首列是状态字母，占住图标列，没有展开箭头槽位。
      lead={
        <>
          <span
            data-status-letter
            aria-hidden="true"
            className={cn(
              "w-3 shrink-0 text-center font-mono text-3xs font-bold",
              meta.className,
            )}
          >
            {meta.letter}
          </span>
          <span className="sr-only">{statusLabel}</span>
          <FileTypeIcon path={path} />
        </>
      }
      trailing={
        <>
          {/*
            窄栏下先挤掉的是目录后缀：它的 shrink 权重大到会先缩到没有，文件名
            因此实际上永不被截断（决策 12）；只有 basename 自己就超宽时才轮到它
            省略。目录后缀从头截断（rtl 让省略号落在开头），根目录下的文件为空。
          */}
          <span
            dir="rtl"
            className="min-w-0 flex-1 shrink-[9999] truncate text-left font-mono text-3xs opacity-55"
          >
            {dir}
          </span>
          <DiffBadge plus={plus} minus={minus} />
        </>
      }
      testId={testId}
      rowData={rowData}
    />
  );
}

/** 两者皆为 0 时不出角标（二进制文件没有可信行数，调用方传 0）。 */
function DiffBadge({ plus, minus }: { plus: number; minus: number }) {
  if (plus <= 0 && minus <= 0) return null;
  return (
    <span
      aria-hidden="true"
      className="inline-flex shrink-0 items-center gap-1 font-mono text-3xs font-medium"
    >
      {plus > 0 ? <span className="text-status-running">+{plus}</span> : null}
      {minus > 0 ? <span className="text-destructive">−{minus}</span> : null}
    </span>
  );
}
