import * as React from "react";
import { Ban, ChevronDown, ChevronRight, GripVertical, X } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import type { OrgSortableRowBinding } from "./drag-binding";
import {
  ORG_EXEC_TARGET_DESTRUCTIVE_REASONS,
  orgBackendTypeLabel,
  orgExecTargetMachineLabel,
  orgExecTargetReasonLabel,
} from "./exec-target-reasons";
import type { OrgBackendModel } from "./types";

/** 一档的可用性判定（后端 ExecTargetAvailabilityView 的两个字段）。 */
export type OrgExecTargetStatus = {
  available: boolean;
  reason: string;
};

/**
 * 执行目标**一档一行**：行上给机器、后端与状态摘要，技能折在行内。
 *
 * 「当前生效」的一档在视觉上与其余区分（左侧一道色条，不只靠徽标）；**不可用**的
 * 一档留在列表里并给出原因，不隐藏 —— 隐藏了用户不知道为什么轮不到它。
 *
 * 行自己不取数：可用性、技能支不支持、技能块本体都由宿主算好递进来（桌面端走
 * Wails 能力矩阵，agentre-server 走 REST）。
 */
export type OrgExecTargetRowProps = {
  index: number;
  total: number;
  backend: OrgBackendModel | undefined;
  status: OrgExecTargetStatus | undefined;
  isFirstAvailable: boolean;
  /**
   * 上移 / 下移一格。**行上不为它们各画一个按钮**（mockup 的行只有一个柄）——
   * 它们是柄上方向键的落点，也是「这一行能不能排序」的判据：给了任一个，柄就画。
   */
  onMoveUp?: () => void;
  onMoveDown?: () => void;
  onRemove?: () => void;
  /**
   * 单档专用：「更换」打开的那份面板（后端选择器，宿主给）。收一个 `close`
   * 而不是直接收节点 —— 选中一项之后要把浮层关掉，而「什么算选中」只有宿主知道。
   */
  replacePanel?: (close: () => void) => React.ReactNode;
  /** 这一档的后端支不支持技能。不支持就不给展开入口，只留一句话说明为什么。 */
  skillsSupported: boolean;
  /** 展开后的技能块（发现来源与授权都要拉后端，因此由宿主给）。 */
  skillsBlock?: React.ReactNode;
  drag?: OrgSortableRowBinding;
};

export function OrgExecTargetRow(props: OrgExecTargetRowProps) {
  const { t } = useUiTranslation();
  const [replaceOpen, setReplaceOpen] = React.useState(false);
  const single = props.total === 1;
  const drag = props.drag;
  const handle = drag?.handle;
  // 这一行能不能排序，看的是宿主给没给重排回调 —— 不是「档数 > 1」：首档没有
  // `onMoveUp`、末档没有 `onMoveDown`，但两者都能动。
  const reorderable = Boolean(props.onMoveUp || props.onMoveDown);

  // R15 的键盘等价物：柄聚焦后直接按 ↑/↓ 就移动这一行，不必先按空格「提起」。
  // 空格提起后的方向键仍走宿主的 KeyboardSensor（listeners 里的那份），两条路径
  // 最终都落到同一个重排上。
  //
  // 行上**只有柄这一个排序控件**（mockup：一档一行，行首一个 ⣿，没有上下箭头），
  // 所以柄不能随「宿主接没接拖拽」消失 —— 那样不拖拽的宿主就一个排序入口都没有。
  // 它因此有两副面孔：接了拖拽是拖拽激活器（cursor-grab + listeners），没接就是一枚
  // **纯键盘**的重排控件，光标与无障碍名都如实说明它只吃方向键，不假装能拖。
  const onHandleKeyDown = (e: React.KeyboardEvent<HTMLElement>) => {
    if (!drag?.isDragging && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
      e.preventDefault();
      if (e.key === "ArrowUp") props.onMoveUp?.();
      else props.onMoveDown?.();
      return;
    }
    handle?.listeners?.onKeyDown?.(e);
  };

  const b = props.backend;
  const local = orgExecTargetMachineLabel(
    b,
    t("org.agent.execTargets.localMachine"),
    t("org.agent.execTargets.reasons.unpaired"),
  );
  const sub = b
    ? `${orgBackendTypeLabel(b.type)} · ${b.name}`
    : t("org.agent.execTargets.reasons.unknownBackend");

  return (
    <div
      ref={drag?.setNodeRef}
      style={drag?.style}
      className={cn(
        "flex min-w-0 flex-col border-border bg-input-bg px-2.5 py-2",
        props.index < props.total - 1 && "border-b",
        // 当前生效的一档在视觉上与其余区分：左侧一道色条，不只靠徽标。
        props.isFirstAvailable && "border-l-2 border-l-status-running",
      )}
      data-testid={`exec-target-row-${props.index}`}
    >
      <div className="flex min-w-0 items-start gap-2">
        {reorderable && (
          <button
            type="button"
            ref={handle?.ref}
            aria-label={t(
              drag
                ? "org.agent.execTargets.dragHandle"
                : "org.agent.execTargets.keyboardHandle",
            )}
            {...(handle?.attributes ?? {})}
            {...(handle?.listeners ?? {})}
            onKeyDown={onHandleKeyDown}
            className={cn(
              "mt-0.5 shrink-0 touch-none select-none rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40",
              drag && "cursor-grab",
            )}
          >
            <GripVertical className="size-3.5" aria-hidden="true" />
          </button>
        )}
        {!single && !reorderable && (
          // 钉住不动的那一档（宿主两个重排回调都没给）不画柄 —— 没有可做的事就不该
          // 有控件。但要留一枚等宽占位：少画一个柄会让这一行的左缘比邻居往左缩，整列
          // 参差（与 OrgAgentRow 对不可拖动行的处理同一条判据）。
          <span className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
        )}
        {!single && (
          <span className="mt-px inline-flex size-5 shrink-0 items-center justify-center rounded-sm bg-secondary font-mono text-2xs text-muted-foreground">
            {props.index + 1}
          </span>
        )}
        {/* 详情面板只有 380px 宽：长机器名在行内截断，不换行把行撑高、也不把面板顶出
          横向滚动。 */}
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-xs font-semibold">{local}</span>
          <span className="truncate font-mono text-2xs text-muted-foreground">
            {sub}
          </span>
        </div>
        <ExecTargetStatusBadge
          status={props.status}
          isFirstAvailable={props.isFirstAvailable}
          isRemote={Boolean(b?.deviceId)}
        />
        {props.onRemove && (
          <button
            type="button"
            aria-label={t("org.agent.execTargets.remove")}
            onClick={props.onRemove}
            className="shrink-0 text-muted-foreground hover:text-destructive"
          >
            <X className="size-3.5" />
          </button>
        )}
        {props.replacePanel && (
          <Popover open={replaceOpen} onOpenChange={setReplaceOpen}>
            <PopoverTrigger asChild>
              <button
                type="button"
                className="shrink-0 text-2xs text-muted-foreground hover:underline"
              >
                {t("org.agent.execTargets.replace")}
              </button>
            </PopoverTrigger>
            <PopoverContent className="w-72 p-0">
              {props.replacePanel(() => setReplaceOpen(false))}
            </PopoverContent>
          </Popover>
        )}
      </div>
      <ExecTargetSkillsFold
        total={props.total}
        machine={local}
        supported={props.skillsSupported}
        block={props.skillsBlock}
      />
    </div>
  );
}

// ExecTargetSkillsFold 是折在行内的那一小块技能：
//   · backend 不支持技能 —— 不给展开入口，只留一句话说明为什么（展开后给一句
//     空话是这一轮要去掉的东西）；
//   · 支持 —— 一个可折叠的入口，展开后就是宿主给的那一块（离线时只减不增）。
// 单档时默认展开：一档没有「扫一眼列表」的需求，折起来只是多一次点击。
function ExecTargetSkillsFold(props: {
  total: number;
  machine: string;
  supported: boolean;
  block?: React.ReactNode;
}) {
  const { t } = useUiTranslation();
  const [expanded, setExpanded] = React.useState(props.total === 1);

  if (!props.supported) {
    return (
      <p
        className="mt-1 flex items-center gap-1 pl-0.5 font-mono text-2xs text-muted-foreground"
        title={t("org.agent.skillsGate.description")}
      >
        <Ban className="size-3 shrink-0" aria-hidden="true" />
        <span className="truncate">{t("org.agent.skillsGate.title")}</span>
      </p>
    );
  }

  return (
    <div className="mt-1 min-w-0">
      <button
        type="button"
        aria-expanded={expanded}
        aria-label={t("org.agent.execTargets.skillsToggle", {
          machine: props.machine,
        })}
        onClick={() => setExpanded((v) => !v)}
        className="inline-flex items-center gap-1 rounded-sm font-mono text-2xs text-muted-foreground hover:text-foreground"
      >
        {expanded ? (
          <ChevronDown className="size-3" aria-hidden="true" />
        ) : (
          <ChevronRight className="size-3" aria-hidden="true" />
        )}
        <span>{t("org.agent.skills.title")}</span>
      </button>
      {expanded && <div className="mt-1 min-w-0">{props.block}</div>}
    </div>
  );
}

function ExecTargetStatusBadge(props: {
  status: OrgExecTargetStatus | undefined;
  isFirstAvailable: boolean;
  isRemote: boolean;
}) {
  const { t } = useUiTranslation();
  if (!props.status) return null;
  if (!props.status.available) {
    const destructive = ORG_EXEC_TARGET_DESTRUCTIVE_REASONS.has(
      props.status.reason,
    );
    return (
      <span
        className={cn(
          "inline-flex shrink-0 items-center rounded-sm px-1.5 py-0.5 text-2xs font-medium",
          destructive
            ? "bg-destructive-soft text-destructive"
            : "border border-border text-muted-foreground",
        )}
      >
        {orgExecTargetReasonLabel(props.status.reason, t)}
      </span>
    );
  }
  if (props.isFirstAvailable) {
    return (
      <span className="inline-flex shrink-0 items-center rounded-sm bg-status-running-bg px-1.5 py-0.5 text-2xs font-medium text-status-running">
        {t("org.agent.execTargets.currentlyActive")}
      </span>
    );
  }
  if (props.isRemote) {
    return (
      <span className="inline-flex shrink-0 items-center rounded-sm bg-secondary px-1.5 py-0.5 text-2xs font-medium text-secondary-foreground">
        {t("org.agent.execTargets.online")}
      </span>
    );
  }
  return null;
}
