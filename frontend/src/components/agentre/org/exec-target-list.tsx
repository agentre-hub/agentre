import * as React from "react";
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  useSensor,
  useSensors,
  type DragEndEvent,
} from "@dnd-kit/core";
import {
  SortableContext,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from "@dnd-kit/sortable";
import { AlertTriangle, Plus } from "lucide-react";
import { useTranslation } from "react-i18next";

import {
  orgBackendTypeLabel,
  OrgExecTargetRow,
  orgExecTargetMachineLabel,
  orgExecTargetReasonLabel,
  useUiTranslation,
  type OrgSortableRowBinding,
} from "@agentre-ai/agentre-ui";

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
  department_svc,
} from "../../../../wailsjs/go/models";

import { useBackendCapabilities } from "../capability/use-backend-capabilities";

import { moveItem } from "./exec-target-reorder";
import {
  ExecTargetSkillsBlock,
  type CopySource,
} from "./exec-target-skills-block";

// ExecTargetRow 是 ExecTargetList 编辑态的一项：agentBackendId 定位这一档，在
// targets 数组内唯一（前端"+ 添加"面板与后端 agent_svc.buildExecTargets 都靠它
// 判重）。skills 是这一档自己的技能授权，因为技能折在行内，列表要读得到它；
// 但**写回**仍然只经 onSkillsChange，列表自己不改这份数组。
export type ExecTargetRow = {
  agentBackendId: number;
  skills?: department_svc.AgentSkillDTO[];
};

// 顺序 / 集合两条写路径回给调用方的都是「只有 agentBackendId」的行：技能不由这两条
// 路径搬运，把它捎带出去会让调用方分不清这一次到底改了什么。
function orderOnly(rows: ExecTargetRow[]): ExecTargetRow[] {
  return rows.map((r) => ({ agentBackendId: r.agentBackendId }));
}

type Props = {
  agentId: number;
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
  // onSkillsChange = 某一档自己的技能授权变了（第三条写路径，按 backend id 定位，
  // 不按下标：行序是本端解析后的顺序，账号级集合的下标与它不是一回事）。
  onSkillsChange: (
    agentBackendId: number,
    next: department_svc.AgentSkillDTO[],
  ) => void;
  saveRejected?: boolean;
  // loading = 顺序数据尚未到达，渲染骨架行。它与「这个 Agent 没有执行目标」的空态
  // 是两件事：混在一起就会同时说出两句互相否定的话。
  loading?: boolean;
};

// 骨架行条数：加载窗口只是占位，取列表最常见的档数，避免加载完成时高度大幅跳变。
const SKELETON_ROWS = 2;

export function ExecTargetList(props: Props) {
  const { t } = useTranslation();
  // 机器名与不可用原因的文案随行本体一起进了共享包，所以这两处要用包的 t。
  const { t: uiT } = useUiTranslation();
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
    props.onReorder(orderOnly(next));
    setAnnouncement(
      t("org.agent.execTargets.moved", {
        position: to + 1,
        total: next.length,
      }),
    );
  };

  const removeAt = (idx: number) => {
    props.onChange(orderOnly(props.targets.filter((_, i) => i !== idx)));
  };

  const appendBackend = (backendId: number) => {
    props.onChange([
      ...orderOnly(props.targets),
      { agentBackendId: backendId },
    ]);
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

  // 技能「从别的档抄一份」的可选来源：同一份列表里除自己以外的档，类型不同的
  // 置灰（包 id 对不上）。列表自己就握着 targets + backends，不必再从外面传进来。
  const copySourcesFor = (index: number): CopySource[] => {
    const own = backendById.get(props.targets[index]?.agentBackendId ?? 0);
    return props.targets
      .map((target, i) => ({ target, i }))
      .filter(({ i }) => i !== index)
      .map(({ target, i }) => {
        const b = backendById.get(target.agentBackendId);
        return {
          index: i,
          label: `${i + 1} · ${orgExecTargetMachineLabel(
            b,
            uiT("org.agent.execTargets.localMachine"),
            uiT("org.agent.execTargets.reasons.unpaired"),
          )}`,
          skills: target.skills ?? [],
          sameType: Boolean(b && own && b.type === own.type),
        };
      });
  };

  const rowPropsFor = (target: ExecTargetRow, index: number): RowProps => ({
    index,
    total: props.targets.length,
    agentId: props.agentId,
    backend: backendById.get(target.agentBackendId),
    status: byBackendId.get(target.agentBackendId),
    skills: target.skills ?? [],
    onSkillsChange: (next) => props.onSkillsChange(target.agentBackendId, next),
    copySources: copySourcesFor(index),
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
        // 「没有执行目标」只说这一条：它同时讲清「不能对话」与「至少要有一项」。
        // 面板此前另有一条 noBackendBound 的 Alert 说同一个条件，两条一起出现。
        <div
          className="rounded-md border border-status-waiting/40 bg-status-waiting-bg px-3 py-4 text-center"
          data-testid="exec-target-empty"
        >
          <p className="flex items-center justify-center gap-1.5 text-sm font-semibold">
            <AlertTriangle className="size-3.5" aria-hidden="true" />
            {t("org.agent.execTargets.emptyTitle")}
          </p>
          <p className="mt-1 text-2xs text-muted-foreground">
            {t("org.agent.execTargets.emptyDescription")}
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
            <ExecTargetRow
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
                    {orgExecTargetMachineLabel(
                      b,
                      uiT("org.agent.execTargets.localMachine"),
                      uiT("org.agent.execTargets.reasons.unpaired"),
                    )}
                    {" —— "}
                    {status ? orgExecTargetReasonLabel(status.reason, uiT) : ""}
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
    <ExecTargetRow
      {...props.row}
      drag={{
        setNodeRef,
        // 拖拽柄是唯一的激活器，避免整行都变成拖拽热区把里面的按钮吞掉。
        handle: {
          ref: setActivatorNodeRef,
          attributes,
          listeners: listeners ?? {},
        },
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
  agentId: number;
  backend: agent_backend_svc.BackendItem | undefined;
  status: { available: boolean; reason: string } | undefined;
  skills: department_svc.AgentSkillDTO[];
  onSkillsChange: (next: department_svc.AgentSkillDTO[]) => void;
  copySources: CopySource[];
  isFirstAvailable: boolean;
  onMoveUp?: () => void;
  onMoveDown?: () => void;
  onRemove?: () => void;
  // 单档场景专用:"更换"按钮打开一份 AddTargetPanel,选中的一项整份替换掉
  // 唯一那一档(而不是追加)。只在 total===1 时给出。
  replaceBackends?: agent_backend_svc.BackendItem[];
  onReplacePick?: (backendId: number) => void;
};

/**
 * 行本体（序号 / 机器 / 状态徽标 / 技能折叠的外壳）在共享包里 —— 两端同一份。
 * 留在这一层的是它按定义要不到的两样东西：**这个 backend 支不支持技能**要拉能力
 * 矩阵，**技能块本体**要拉技能目录，两者都是 Wails 调用。
 */
function ExecTargetRow(props: RowProps & { drag?: OrgSortableRowBinding }) {
  const { caps } = useBackendCapabilities(props.backend?.type);
  // caps 还没到 / 拉失败都按「不支持」渲染（与 R15e 的 ExecTargetSkillsBlock 同一
  // 判据），不给一个点开是空的入口。
  const skillsSupported = caps?.has("skills") ?? false;
  const replaceBackends = props.replaceBackends;
  const onReplacePick = props.onReplacePick;

  return (
    <OrgExecTargetRow
      index={props.index}
      total={props.total}
      backend={props.backend}
      status={props.status}
      isFirstAvailable={props.isFirstAvailable}
      onMoveUp={props.onMoveUp}
      onMoveDown={props.onMoveDown}
      onRemove={props.onRemove}
      replacePanel={
        replaceBackends && onReplacePick
          ? (close) => (
              <AddTargetPanel
                backends={replaceBackends}
                usedIds={new Set()}
                onPick={(id) => {
                  onReplacePick(id);
                  close();
                }}
              />
            )
          : undefined
      }
      skillsSupported={skillsSupported}
      skillsBlock={
        <ExecTargetSkillsBlock
          agentId={props.agentId}
          index={props.index}
          total={props.total}
          backend={props.backend}
          skills={props.skills}
          onSkillsChange={props.onSkillsChange}
          copySources={props.copySources}
        />
      }
      drag={props.drag}
    />
  );
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
            <div className="px-3 pb-1 pt-2 text-2xs font-semibold text-subtle-foreground">
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
                      {orgBackendTypeLabel(b.type)} · {b.name}
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
