import {
  Check,
  Circle,
  CircleHelp,
  CircleSlash,
  FilePlus,
  FileX,
  Hourglass,
  LoaderCircle,
  Pencil,
  Plus,
  ShieldCheck,
  SkipForward,
  Trash2,
  TriangleAlert,
  X,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";

/**
 * 「状态 → 图标」对照表的唯一实现（docs/design.md §6.4）。
 *
 * 颜色按 §3.5 的角色取：独立图标用饱和的 `status-*`；修改用 `primary-text`；
 * 其余终态退到 `muted-foreground`。同色的状态靠图标形状区分，不只靠颜色。
 */
const STATUS_MARKS = {
  running: { icon: LoaderCircle, tone: "text-status-running" },
  done: { icon: Check, tone: "text-status-running" },
  failed: { icon: TriangleAlert, tone: "text-status-error" },
  error: { icon: TriangleAlert, tone: "text-status-error" },
  stopped: { icon: CircleSlash, tone: "text-muted-foreground" },
  cancelled: { icon: CircleSlash, tone: "text-muted-foreground" },
  unknown: { icon: CircleHelp, tone: "text-muted-foreground" },
  skipped: { icon: SkipForward, tone: "text-muted-foreground" },
  expired: { icon: Hourglass, tone: "text-muted-foreground" },
  invalidated: { icon: Hourglass, tone: "text-muted-foreground" },
  queued: { icon: Circle, tone: "text-muted-foreground" },
  approved: { icon: Check, tone: "text-status-running" },
  allowed: { icon: ShieldCheck, tone: "text-status-running" },
  denied: { icon: X, tone: "text-status-error" },
  created: { icon: Plus, tone: "text-status-running" },
  updated: { icon: Pencil, tone: "text-primary-text" },
  deleted: { icon: Trash2, tone: "text-status-error" },
  fileNew: { icon: FilePlus, tone: "text-status-running" },
  fileDeleted: { icon: FileX, tone: "text-status-error" },
} satisfies Record<string, { icon: LucideIcon; tone: string }>;

type StatusMarkState = keyof typeof STATUS_MARKS;

type StatusMarkProps = {
  state: StatusMarkState;
  className?: string;
};

/**
 * 纯图标的状态标记：一个状态一个图标，自带界面语言的可访问名与同文案的悬停提示。
 * 被它替换掉的可见文字原样成为可访问名，所在行由行内文本算出的名字因此不变。
 *
 * 进行中的转圈在「减少动态效果」下静止（§8）。
 */
function StatusMark({ state, className }: StatusMarkProps) {
  const { t } = useUiTranslation();
  const { icon: Icon, tone } = STATUS_MARKS[state];
  const name = t(`statusName.${state}`);

  return (
    <span
      role="img"
      aria-label={name}
      title={name}
      data-slot="status-mark"
      data-state={state}
      className={cn(
        "inline-flex size-3.5 shrink-0 items-center justify-center",
        tone,
        className,
      )}
    >
      <Icon
        aria-hidden="true"
        className={cn(
          "size-full",
          state === "running" && "animate-spin motion-reduce:animate-none",
        )}
      />
    </span>
  );
}

export { StatusMark };
export type { StatusMarkProps, StatusMarkState };
