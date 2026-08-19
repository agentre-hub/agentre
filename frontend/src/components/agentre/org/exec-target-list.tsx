import * as React from "react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DraggableAttributes,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { ChevronDown, ChevronUp, GripVertical, Plus, X } from "lucide-react";
import { useTranslation } from "react-i18next";

import { Button } from "@/components/ui/button";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { cn } from "@/lib/utils";

import type {
  agent_backend_svc,
  chat_svc,
} from "../../../../wailsjs/go/models";

import { execTargetReasonLabel } from "./exec-target-reasons";
import { moveItem } from "./exec-target-reorder";

// ExecTargetRow 是 ExecTargetList 编辑态的一项：这里只关心 agentBackendId——
// 技能授权（ExecTargetInputDTO.skills）由 org-detail-agent.tsx 在同一个数组元素上
// 一并持有，ExecTargetList 不读也不写那部分，靠 agentBackendId 在 targets 数组内
// 唯一这条不变量对齐（前端"+ 添加"面板与后端 agent_svc.buildExecTargets 都靠它
// 判重）。
export type ExecTargetRow = { agentBackendId: number };

type Props = {
  agentName: string;
  // availability 由面板自己那一份 useExecTargetAvailability 下传，列表不再自持一份。
  // 两处各读一次不只是白发一趟 Wails 调用：两份副本各自独立地从「加载中」走到
  // 「落定」，列表那份先落定时会用它自己的判定渲染「全部不可用」横幅，而面板那份
  // 还没到、行还是骨架——横幅就压在骨架之上。判定只有一个来源才不会自相矛盾。
  availability: Map<number, chat_svc.ExecTargetAvailabilityView>;
  // targets 是这台电脑当前实际的派发顺序（后端解析后的顺序），也是唯一的一份列表：
  // 界面上不再有第二个「账号默认顺序」视图。
  targets: ExecTargetRow[];
  backends: agent_backend_svc.BackendItem[];
  // onChange = 集合变更（添加 / 移除 / 更换），写的是账号级执行目标集合。
  onChange: (next: ExecTargetRow[]) => void;
  // onReorder = 顺序变更（拖拽 / 拖拽柄方向键 / 上移下移），只写本端顺序。
  // 两条写路径由列表自己分开给出，而不是让调用方回头比对数组去猜刚才发生了什么。
  onReorder: (next: ExecTargetRow[]) => void;
  saveRejected?: boolean;
  // loading = 顺序数据尚未到达，渲染骨架行。它与「这个 Agent 没有执行目标」的空态
  // 是两件事：混在一起就会同时说出两句互相否定的话。
  loading?: boolean;
};

// 骨架行条数：加载窗口只是占位，取列表最常见的档数，避免加载完成时高度大幅跳变。
const SKELETON_ROWS = 2;

const BACKEND_TYPE_LABEL: Record<string, string> = {
  claudecode: "Claude Code",
  "claude-code": "Claude Code",
  codex: "Codex",
  builtin: "Built-in",
  openclaw: "OpenClaw",
  piagent: "Pi Agent",
};

function backendTypeLabel(type: string): string {
  return BACKEND_TYPE_LABEL[type] ?? type;
}

function machineLabel(
  b: agent_backend_svc.BackendItem | undefined,
  localLabel: string,
  unresolvedLabel: string,
): string {
  if (!b || !b.deviceId) return localLabel;
  return b.deviceName || unresolvedLabel;
}

// 不可用原因里哪些是"供应商/配置类"问题（红色强调），哪些是"连通性类"问题
// （中性提示）——与 R15 的判据分组一致（决策 37 的既有 BlockReason vs R15b 新增的
// exec-target-* 三类）。
const DESTRUCTIVE_REASONS = new Set([
  "no-backend",
  "backend-requires-provider",
  "provider-inactive",
  "remote-provider-missing",
  "gateway-not-running",
  "remote-openclaw-unavailable",
  "unknown-backend",
]);

export function ExecTargetList(props: Props) {
  const { t } = useTranslation();
  const [addOpen, setAddOpen] = React.useState(false);
  const [announcement, setAnnouncement] = React.useState("");

  const byBackendId = props.availability;

  const backendById = React.useMemo(() => {
    const m = new Map<number, agent_backend_svc.BackendItem>();
    for (const b of props.backends) m.set(b.id, b);
    return m;
  }, [props.backends]);

  const firstAvailableIndex = props.targets.findIndex(
    (t) => byBackendId.get(t.agentBackendId)?.available === true,
  );
  const allEvaluated =
    props.targets.length > 0 &&
    props.targets.every((t) => byBackendId.has(t.agentBackendId));
  const allUnavailable = allEvaluated && firstAvailableIndex === -1;

  const move = (from: number, to: number) => {
    const next = moveItem(props.targets, from, to);
    if (next === props.targets) return;
    props.onReorder(next);
    setAnnouncement(
      t("org.agent.execTargets.moved", {
        position: to + 1,
        total: next.length,
      }),
    );
  };

  const removeAt = (idx: number) => {
    props.onChange(props.targets.filter((_, i) => i !== idx));
  };

  const appendBackend = (backendId: number) => {
    props.onChange([...props.targets, { agentBackendId: backendId }]);
    setAddOpen(false);
  };

  const replaceSole = (backendId: number) => {
    props.onChange([{ agentBackendId: backendId }]);
  };

  const usedIds = new Set(props.targets.map((t) => t.agentBackendId));
  const single = props.targets.length === 1;

  // 拖拽与「上移/下移」按钮、拖拽柄上的 ↑/↓ 全部收敛到同一个 move()（内部是
  // exec-target-reorder 的 moveItem），排序语义只有一份。
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, {
      coordinateGetter: sortableKeyboardCoordinates,
    }),
  );

  const handleDragEnd = (e: DragEndEvent) => {
    if (!e.over || e.active.id === e.over.id) return;
    const idOf = (raw: string | number) =>
      props.targets.findIndex((t) => String(t.agentBackendId) === String(raw));
    const from = idOf(e.active.id);
    const to = idOf(e.over.id);
    if (from < 0 || to < 0) return;
    move(from, to);
  };

  const rowPropsFor = (target: ExecTargetRow, index: number): RowProps => ({
    index,
    total: props.targets.length,
    backend: backendById.get(target.agentBackendId),
    status: byBackendId.get(target.agentBackendId),
    // R20：只有一项时「当前生效」徽标不出现——一档时它零信息量，
    // 与序号/拖拽柄/排序说明同批隐去。
    isFirstAvailable: !single && index === firstAvailableIndex,
    onMoveUp: index > 0 ? () => move(index, index - 1) : undefined,
    onMoveDown:
      index < props.targets.length - 1
        ? () => move(index, index + 1)
        : undefined,
    // 增删/更换与顺序无关，因此在这唯一一份列表上恒可用（R15：单档时不给移除，
    // 「至少要有一项」那条约束靠它成立，改成「更换」）。
    onRemove: !single ? () => removeAt(index) : undefined,
    replaceBackends: single ? props.backends : undefined,
    onReplacePick: single ? replaceSole : undefined,
  });

  return (
    <div className="flex flex-col gap-2.5">
      <div className="flex items-center gap-2">
        <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
          {t("org.agent.execTargets.title")}
        </h3>
        <div className="flex-1" />
        {/* 骨架期间不给「添加」：此刻列表还不知道自己有哪些档，基于空列表的一次
            添加会把账号级集合整份写成新加的那一项。 */}
        {!props.loading && props.targets.length > 0 && (
          <Popover open={addOpen} onOpenChange={setAddOpen}>
            <PopoverTrigger asChild>
              <Button
                type="button"
                variant="outline"
                size="sm"
                className="h-7 px-2 text-2xs"
              >
                <Plus className="size-3" aria-hidden="true" />
                {t("org.agent.execTargets.add")}
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-72 p-0">
              <AddTargetPanel
                backends={props.backends}
                usedIds={usedIds}
                onPick={appendBackend}
              />
            </PopoverContent>
          </Popover>
        )}
      </div>

      {props.loading ? (
        <ExecTargetSkeleton />
      ) : props.targets.length === 0 ? (
        <div className="rounded-md border border-border bg-input-bg px-3 py-5 text-center">
          <p className="text-sm text-muted-foreground">
            {t("org.agent.execTargets.emptyTitle")}
          </p>
          <p className="mt-1 text-2xs text-muted-foreground">
            {t("org.agent.execTargets.emptyHint")}
          </p>
          <Popover open={addOpen} onOpenChange={setAddOpen}>
            <PopoverTrigger asChild>
              <Button
                type="button"
                size="sm"
                className="mt-2 h-7 px-2 text-2xs"
              >
                <Plus className="size-3" aria-hidden="true" />
                {t("org.agent.execTargets.addFull")}
              </Button>
            </PopoverTrigger>
            <PopoverContent className="w-72 p-0">
              <AddTargetPanel
                backends={props.backends}
                usedIds={usedIds}
                onPick={appendBackend}
              />
            </PopoverContent>
          </Popover>
          {props.saveRejected && (
            <p className="mt-1.5 text-2xs text-destructive">
              {t("org.agent.execTargets.saveRejected")}
            </p>
          )}
        </div>
      ) : (
        <div className="flex flex-col overflow-hidden rounded-md border border-border bg-input-bg">
          {single ? (
            // R20：单档退化成今天的样子——不进 DndContext，也就没有拖拽柄。
            <ExecTargetRowView
              key={props.targets[0].agentBackendId}
              {...rowPropsFor(props.targets[0], 0)}
            />
          ) : (
            <DndContext sensors={sensors} onDragEnd={handleDragEnd}>
              <SortableContext
                items={props.targets.map((t) => String(t.agentBackendId))}
                strategy={verticalListSortingStrategy}
              >
                {props.targets.map((target, index) => (
                  <SortableExecTargetRow
                    key={target.agentBackendId}
                    id={String(target.agentBackendId)}
                    row={rowPropsFor(target, index)}
                  />
                ))}
              </SortableContext>
            </DndContext>
          )}
        </div>
      )}

      {allUnavailable && (
        <div className="flex gap-2 rounded-md border border-destructive bg-destructive-soft p-3">
          <div className="flex min-w-0 flex-col gap-1">
            <span className="text-xs font-semibold">
              {t("org.agent.execTargets.allUnavailableTitle", {
                name: props.agentName,
              })}
            </span>
            <span className="text-2xs leading-relaxed text-muted-foreground">
              {props.targets.map((target, i) => {
                const status = byBackendId.get(target.agentBackendId);
                const b = backendById.get(target.agentBackendId);
                return (
                  <React.Fragment key={target.agentBackendId}>
                    {i + 1} ·{" "}
                    {machineLabel(
                      b,
                      t("org.agent.execTargets.localMachine"),
                      t("org.agent.execTargets.reasons.unpaired"),
                    )}
                    {" —— "}
                    {status ? execTargetReasonLabel(status.reason, t) : ""}
                    <br />
                  </React.Fragment>
                );
              })}
            </span>
          </div>
        </div>
      )}

      {/* 本列表自己的播报（按钮 / 拖拽柄方向键都走它）。DndContext 另有一个它自己
          的 role="status" 活动区用于拖拽过程，两者并存，靠 testid 区分。 */}
      <p role="status" data-testid="exec-target-announcer" className="sr-only">
        {announcement}
      </p>
    </div>
  );
}

// ExecTargetSkeleton 是顺序数据还在路上时的占位：行框架照常在，只是内容还没到。
// 它替代的是此前那张空态卡片——「还没有执行目标」在加载窗口里是假话。
function ExecTargetSkeleton() {
  const { t } = useTranslation();
  return (
    <div
      className="flex flex-col overflow-hidden rounded-md border border-border bg-input-bg"
      data-testid="exec-target-skeleton"
    >
      <span role="status" className="sr-only">
        {t("org.agent.execTargets.loading")}
      </span>
      {Array.from({ length: SKELETON_ROWS }, (_, i) => (
        <div
          key={i}
          aria-hidden="true"
          className={cn(
            "flex items-center gap-2 border-border px-2.5 py-2",
            i < SKELETON_ROWS - 1 && "border-b",
          )}
        >
          <div className="flex min-w-0 flex-1 flex-col gap-1.5">
            <div className="h-2.5 w-24 rounded-sm bg-muted motion-safe:animate-pulse" />
            <div className="h-2 w-32 rounded-sm bg-muted motion-safe:animate-pulse" />
          </div>
          <div className="h-4 w-12 shrink-0 rounded-sm bg-muted motion-safe:animate-pulse" />
        </div>
      ))}
    </div>
  );
}

// DragBinding 是 useSortable 交给行视图的那一小撮东西：整行的 ref/位移样式，
// 以及只挂在拖拽柄上的 attributes/listeners（拖拽柄是唯一的激活器，避免整行
// 都变成拖拽热区把里面的按钮吞掉）。
type DragBinding = {
  setNodeRef: (node: HTMLElement | null) => void;
  setActivatorNodeRef: (node: HTMLElement | null) => void;
  attributes: DraggableAttributes;
  listeners: React.DOMAttributes<HTMLElement>;
  style: React.CSSProperties;
  isDragging: boolean;
};

function SortableExecTargetRow(props: { id: string; row: RowProps }) {
  const {
    attributes,
    listeners,
    setNodeRef,
    setActivatorNodeRef,
    transform,
    transition,
    isDragging,
  } = useSortable({ id: props.id });
  return (
    <ExecTargetRowView
      {...props.row}
      drag={{
        setNodeRef,
        setActivatorNodeRef,
        attributes,
        listeners: listeners ?? {},
        style: {
          transform: transform
            ? `translate3d(0, ${transform.y}px, 0)`
            : undefined,
          transition,
          opacity: isDragging ? 0.5 : undefined,
        },
        isDragging,
      }}
    />
  );
}

type RowProps = {
  index: number;
  total: number;
  backend: agent_backend_svc.BackendItem | undefined;
  status: { available: boolean; reason: string } | undefined;
  isFirstAvailable: boolean;
  onMoveUp?: () => void;
  onMoveDown?: () => void;
  onRemove?: () => void;
  // 单档场景专用:"更换"按钮打开一份 AddTargetPanel,选中的一项整份替换掉
  // 唯一那一档(而不是追加)。只在 total===1 时给出。
  replaceBackends?: agent_backend_svc.BackendItem[];
  onReplacePick?: (backendId: number) => void;
  drag?: DragBinding;
};

function ExecTargetRowView(props: RowProps) {
  const { t } = useTranslation();
  const [replaceOpen, setReplaceOpen] = React.useState(false);
  const single = props.total === 1;
  const drag = props.drag;

  // R15 的键盘等价物：拖拽柄聚焦后直接按 ↑/↓ 就移动这一行，不必先按空格「提起」。
  // 空格提起后的方向键仍走 dnd-kit 的 KeyboardSensor（listeners 里的那份），两条
  // 路径最终都落到同一个 moveItem 上。
  const onHandleKeyDown = (e: React.KeyboardEvent<HTMLElement>) => {
    if (!drag?.isDragging && (e.key === "ArrowUp" || e.key === "ArrowDown")) {
      e.preventDefault();
      if (e.key === "ArrowUp") props.onMoveUp?.();
      else props.onMoveDown?.();
      return;
    }
    drag?.listeners?.onKeyDown?.(e);
  };
  const b = props.backend;
  const local = machineLabel(
    b,
    t("org.agent.execTargets.localMachine"),
    t("org.agent.execTargets.reasons.unpaired"),
  );
  const sub = b
    ? `${backendTypeLabel(b.type)} · ${b.name}`
    : t("org.agent.execTargets.reasons.unknownBackend");

  return (
    <div
      ref={drag?.setNodeRef}
      style={drag?.style}
      className={cn(
        "flex items-start gap-2 border-border bg-input-bg px-2.5 py-2",
        props.index < props.total - 1 && "border-b",
      )}
      data-testid={`exec-target-row-${props.index}`}
    >
      {!single && (
        <button
          type="button"
          ref={drag?.setActivatorNodeRef}
          aria-label={t("org.agent.execTargets.dragHandle")}
          {...(drag?.attributes ?? {})}
          {...(drag?.listeners ?? {})}
          onKeyDown={onHandleKeyDown}
          className="mt-0.5 shrink-0 cursor-grab touch-none select-none rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
        >
          <GripVertical className="size-3.5" aria-hidden="true" />
        </button>
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
      {!single && (
        <div className="flex shrink-0 flex-col">
          <button
            type="button"
            aria-label={t("org.agent.execTargets.moveUp")}
            disabled={!props.onMoveUp}
            onClick={props.onMoveUp}
            className="text-muted-foreground hover:text-foreground disabled:opacity-30"
          >
            <ChevronUp className="size-3.5" />
          </button>
          <button
            type="button"
            aria-label={t("org.agent.execTargets.moveDown")}
            disabled={!props.onMoveDown}
            onClick={props.onMoveDown}
            className="text-muted-foreground hover:text-foreground disabled:opacity-30"
          >
            <ChevronDown className="size-3.5" />
          </button>
        </div>
      )}
      <StatusBadge
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
      {props.replaceBackends && props.onReplacePick && (
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
            <AddTargetPanel
              backends={props.replaceBackends}
              usedIds={new Set()}
              onPick={(id) => {
                props.onReplacePick?.(id);
                setReplaceOpen(false);
              }}
            />
          </PopoverContent>
        </Popover>
      )}
    </div>
  );
}

function StatusBadge(props: {
  status: { available: boolean; reason: string } | undefined;
  isFirstAvailable: boolean;
  isRemote: boolean;
}) {
  const { t } = useTranslation();
  if (!props.status) return null;
  if (!props.status.available) {
    const destructive = DESTRUCTIVE_REASONS.has(props.status.reason);
    return (
      <span
        className={cn(
          "inline-flex shrink-0 items-center rounded-sm px-1.5 py-0.5 text-2xs font-medium",
          destructive
            ? "bg-destructive-soft text-destructive"
            : "border border-border text-muted-foreground",
        )}
      >
        {execTargetReasonLabel(props.status.reason, t)}
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

function AddTargetPanel(props: {
  backends: agent_backend_svc.BackendItem[];
  usedIds: Set<number>;
  onPick: (backendId: number) => void;
}) {
  const { t } = useTranslation();
  const [query, setQuery] = React.useState("");

  const groups = React.useMemo(() => {
    const q = query.trim().toLowerCase();
    const filtered = props.backends.filter(
      (b) =>
        !q ||
        b.name.toLowerCase().includes(q) ||
        (b.deviceName ?? "").toLowerCase().includes(q),
    );
    const byGroup = new Map<string, agent_backend_svc.BackendItem[]>();
    for (const b of filtered) {
      const key = b.deviceId || "";
      const list = byGroup.get(key) ?? [];
      list.push(b);
      byGroup.set(key, list);
    }
    return byGroup;
  }, [props.backends, query]);

  return (
    <div>
      <div className="border-b border-border bg-input-bg px-3 py-2">
        <input
          value={query}
          onChange={(e) => setQuery(e.target.value)}
          placeholder={t("org.agent.execTargets.addPanel.searchPlaceholder")}
          aria-label={t("org.agent.execTargets.addPanel.searchPlaceholder")}
          className="w-full bg-transparent text-xs text-foreground outline-none placeholder:text-muted-foreground"
        />
      </div>
      <div className="max-h-72 overflow-y-auto py-1">
        {[...groups.entries()].map(([deviceId, backends]) => (
          <div key={deviceId || "local"}>
            <div className="px-3 pb-1 pt-2 text-2xs font-semibold text-muted-foreground">
              {deviceId
                ? backends[0].deviceName ||
                  t("org.agent.execTargets.addPanel.unpaired")
                : t("org.agent.execTargets.localMachine")}
            </div>
            {backends.map((b) => {
              const used = props.usedIds.has(b.id);
              const unpaired = Boolean(b.deviceId) && !b.deviceName;
              return (
                <button
                  type="button"
                  key={b.id}
                  disabled={used}
                  onClick={() => props.onPick(b.id)}
                  className={cn(
                    "flex w-full items-center gap-2 px-3 py-1.5 text-left hover:bg-accent",
                    used && "cursor-not-allowed opacity-60",
                  )}
                >
                  <div className="flex min-w-0 flex-1 flex-col">
                    <span className="text-xs font-semibold">
                      {backendTypeLabel(b.type)} · {b.name}
                    </span>
                  </div>
                  {used && (
                    <span className="inline-flex shrink-0 items-center rounded-sm border border-border px-1.5 py-0.5 text-2xs text-muted-foreground">
                      {t("org.agent.execTargets.addPanel.alreadyInList")}
                    </span>
                  )}
                  {!used && unpaired && (
                    <span className="inline-flex shrink-0 items-center rounded-sm border border-border px-1.5 py-0.5 text-2xs text-muted-foreground">
                      {t("org.agent.execTargets.addPanel.unpaired")}
                    </span>
                  )}
                </button>
              );
            })}
          </div>
        ))}
      </div>
    </div>
  );
}
