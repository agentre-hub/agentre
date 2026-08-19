import * as React from "react";
import {
  AlertTriangle,
  Bot,
  Check,
  CornerDownRight,
  Info,
  Network,
  Plus,
  Search,
  Trash2,
  Webhook,
  Wrench,
  X,
} from "lucide-react";
import { useTranslation } from "react-i18next";
import { useNavigate } from "react-router-dom";

import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover";
import { Textarea } from "@/components/ui/textarea";
import { cn } from "@/lib/utils";

import { AgentAvatarPicker, AgentAvatarUploadActions } from "../icon-picker";
import { AgentAvatar } from "../primitives";
import {
  agentColorClassNames,
  type AgentColor,
  agentColorOrder,
} from "../types";
import {
  agent_svc,
  department_svc,
  type agent_backend_svc,
} from "../../../../wailsjs/go/models";

import { useBackendCapabilities } from "../capability/use-backend-capabilities";
import { isValidOrgDrop } from "./org-drop";
import { resolveReportTo } from "./reporting";
import { buildToolList } from "./tool-catalog";
import {
  iconForKey,
  safeAgentColor,
  type OrgAgent,
  type OrgDepartment,
} from "./types";
import { useAutoSave } from "./use-auto-save";
import { AutoSaveStatus } from "./auto-save-status";
import { ExecTargetList, type ExecTargetRow } from "./exec-target-list";
import { useExecTargetAvailability } from "./use-exec-target-availability";

type Props = {
  agent: OrgAgent;
  departments: OrgDepartment[];
  agents: OrgAgent[];
  backends: agent_backend_svc.BackendItem[];
  isLeadOf: OrgDepartment | null;
  availableTools?: string[];
  onUpdate: (req: agent_svc.UpdateAgentRequest) => Promise<unknown>;
  // onMove 是「归属」下拉唯一的写口：与索引里拖拽落点发出的是同一个写操作，
  // 入参一个字段都不差（agent_svc.MoveAgentRequest）。
  onMove: (req: agent_svc.MoveAgentRequest) => Promise<unknown>;
  onDelete: (req: agent_svc.DeleteAgentRequest) => Promise<unknown>;
  onUploadAvatar: (req: agent_svc.UploadAvatarRequest) => Promise<unknown>;
  onDeleteAvatar: (req: agent_svc.DeleteAvatarRequest) => Promise<unknown>;
  onClose: () => void;
};

// ExecTargetEdit 是编辑态执行目标行（R15/R15e）：agentBackendId 定位这一档，
// skills 是它自己的技能授权（下沉到执行目标行,不再是 Agent 级一份）。
type ExecTargetEdit = {
  agentBackendId: number;
  skills: department_svc.AgentSkillDTO[];
};

const SYSTEM_BADGE = "DEFAULT";

// 工具清单每一行行首的那个图标：清单是「一行只有图标、名字、需审批、一句话能力、
// 动作按钮」，图标只用来在扫读时区分行，未登记的 key 落到通用的扳手上。
const TOOL_ICONS: Record<string, typeof Network> = {
  org: Network,
  subagent: Bot,
  hook: Webhook,
};

function execTargetsFromAgent(agent: OrgAgent): ExecTargetEdit[] {
  return (agent.execTargets ?? []).map((t) => ({
    agentBackendId: t.agentBackendId,
    skills: (t.skills ?? []).map((s) => ({ ...s })),
  }));
}

export function OrgDetailAgent(props: Props) {
  const { t } = useTranslation();
  const navigate = useNavigate();
  const isCEO = props.agent.systemBadge === SYSTEM_BADGE;

  const { values, patch, flush, wrap, status, pendingInvalid, retry } =
    useAutoSave({
      initial: {
        name: props.agent.name,
        description: props.agent.description,
        avatarColor: safeAgentColor(props.agent.avatarColor),
        avatarIcon: props.agent.avatarIcon || "",
        execTargets: execTargetsFromAgent(props.agent),
        prompt: (props.agent.prompt ?? []).join("\n"),
        tools: ((): department_svc.AgentToolDTO[] => {
          const cur = new Map(
            (props.agent.tools ?? []).map((tl) => [tl.key, tl.enabled]),
          );
          return (props.availableTools ?? []).map((key) => ({
            key,
            enabled: cur.get(key) ?? false,
          }));
        })(),
      },
      // R15：列表为空的 Agent 不能起会话——界面在保存时就要求至少一项。
      isValid: (v) => v.name.trim() !== "" && v.execTargets.length > 0,
      save: (v) =>
        props.onUpdate(
          agent_svc.UpdateAgentRequest.createFrom({
            id: props.agent.id,
            name: v.name,
            description: v.description,
            avatarColor: v.avatarColor,
            avatarIcon: v.avatarIcon,
            prompt: v.prompt.split("\n").filter((s) => s.trim() !== ""),
            execTargets: v.execTargets,
            tools: v.tools,
          }),
        ),
    });

  const {
    name,
    description,
    avatarColor,
    avatarIcon,
    execTargets,
    prompt,
    tools,
  } = values;

  const [deletePromptOpen, setDeletePromptOpen] = React.useState(false);

  // 汇报对象走统一解析：显式上级 ▸ 部门 leader（沿父部门链递归） ▸ CEO 兜底。
  const reportToId = resolveReportTo(
    props.agent,
    props.agents,
    props.departments,
  );
  const reportTarget =
    reportToId !== 0
      ? (props.agents.find((a) => a.id === reportToId) ?? null)
      : null;

  // props.agent.backend 是这个 Agent 的主档 backend 摘要（department_svc.Load 已
  // join 好，见 AgentItem.backend 字段注释）：props.backends 全量列表还没加载完/
  // 该 backend 已被删但摘要还留着时的兜底,不让界面在这段时间内空白。
  const backendById = React.useMemo(() => {
    const m = new Map<number, agent_backend_svc.BackendItem>();
    for (const b of props.backends) m.set(b.id, b);
    if (props.agent.backend && !m.has(props.agent.backend.id)) {
      m.set(
        props.agent.backend.id,
        backendItemFromSummary(props.agent.backend),
      );
    }
    return m;
  }, [props.backends, props.agent.backend]);
  const backendsForList = React.useMemo(
    () => [...backendById.values()],
    [backendById],
  );

  const singleTarget = execTargets.length === 1;
  const primaryTarget = execTargets[0];
  const selectedBackend = primaryTarget
    ? (backendById.get(primaryTarget.agentBackendId) ??
      (props.agent.backend?.id === primaryTarget.agentBackendId
        ? props.agent.backend
        : undefined))
    : undefined;

  // 任务 8：不可对话状态内联提示（继续只覆盖单档场景——多档时"哪一档缺什么"已经在
  // 执行目标列表的逐行徽标 + 全部不可用横幅里说明，不需要在这里重复一份笼统提示）。
  // 「没有任何执行目标」不在这里说：那一条与列表空态是同一个条件，只由列表说一次。
  const hasUsableProvider =
    selectedBackend?.llmProviderActive === true &&
    Boolean(selectedBackend?.llmProviderName?.trim());
  const builtinMissingProvider =
    singleTarget && selectedBackend?.type === "builtin" && !hasUsableProvider;

  // patchExecTargetSet 把列表上的一次增删/更换落到**账号级集合**上：成员按 next
  // 收敛，顺序取账号自己那一份（execTargets）——列表展示的是本端解析后的顺序，
  // 照搬它会把本端排序回写成账号顺序，污染其它端看到的顺序。
  const patchExecTargetSet = (next: ExecTargetRow[]) => {
    const wanted = next.map((r) => r.agentBackendId);
    const wantedIds = new Set(wanted);
    const kept = execTargets.filter((t) => wantedIds.has(t.agentBackendId));
    const keptIds = new Set(kept.map((t) => t.agentBackendId));
    const added: ExecTargetEdit[] = wanted
      .filter((id) => !keptIds.has(id))
      .map((id) => ({ agentBackendId: id, skills: [] }));
    patch({ execTargets: [...kept, ...added] }, { immediate: true });
  };

  // 本端生效顺序（R14 解析后的顺序，含覆盖 / 无覆盖时本机自己提前）来自后端
  // ListExecTargetAvailability 的返回数组顺序，它就是执行目标区唯一那份列表。
  const deviceTargetsKey = React.useMemo(
    () =>
      execTargets
        .map((t) => t.agentBackendId)
        .slice()
        .sort((a, b) => a - b)
        .join(","),
    [execTargets],
  );
  const availability = useExecTargetAvailability(
    props.agent.id,
    deviceTargetsKey,
  );
  // null = 这一轮读还没落定（顺序还在路上）；数组 = 已落定的本端顺序，读失败时是空
  // 数组。两者必须分开，否则「读完了但没拿到」会被当成「还在路上」，列表永远停在骨架。
  const [deviceTargets, setDeviceTargets] = React.useState<
    ExecTargetRow[] | null
  >(null);
  React.useEffect(() => {
    if (availability.orderedTargets.length > 0) {
      setDeviceTargets(availability.orderedTargets);
    } else if (availability.settled) {
      setDeviceTargets([]);
    }
  }, [availability.orderedTargets, availability.settled]);

  // 顺序数据还没到达：渲染骨架而不是空态卡片（真正的空态是「这个 Agent 没有任何
  // 执行目标」，此时 execTargets 也是空的，下面这个判断自然为假）。
  const orderPending = execTargets.length > 0 && deviceTargets === null;

  // 顺序读完了却没拿到（读失败）时列表回落到账号级集合自己的顺序：增删/更换与顺序
  // 无关，任何状态下都得可用（规格「增删恒可用」），把列表钉死在骨架上等于把它们
  // 一起锁掉。用户少看到的只是「本机档自动提前」那一下重排，能做的事一件不少。
  //
  // 技能挂在账号级集合那一份上（execTargets），顺序来自本端解析，两者按
  // agentBackendId 合起来才是行要渲染的东西 —— 合并只做这一处。
  const skillsByBackendId = React.useMemo(() => {
    const m = new Map<number, department_svc.AgentSkillDTO[]>();
    for (const t of execTargets) m.set(t.agentBackendId, t.skills);
    return m;
  }, [execTargets]);
  const listTargets: ExecTargetRow[] = (
    deviceTargets && deviceTargets.length > 0
      ? deviceTargets
      : execTargets.map((t) => ({ agentBackendId: t.agentBackendId }))
  ).map((row) => ({
    agentBackendId: row.agentBackendId,
    skills: skillsByBackendId.get(row.agentBackendId) ?? [],
  }));

  // 重排只写本端顺序覆盖（orderOverride），不碰账号级集合、不同步。
  const writeDeviceOverride = React.useCallback(
    (order: number[]) => {
      void wrap(() =>
        props.onUpdate(
          agent_svc.UpdateAgentRequest.createFrom({
            id: props.agent.id,
            name,
            description,
            avatarColor,
            avatarIcon,
            prompt: prompt.split("\n").filter((s) => s.trim() !== ""),
            execTargets,
            tools,
            orderOverride: order,
          }),
        ),
      ).then(() => availability.reload());
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [
      props.agent.id,
      name,
      description,
      avatarColor,
      avatarIcon,
      prompt,
      execTargets,
      tools,
    ],
  );

  const handleExecTargetReorder = (next: ExecTargetRow[]) => {
    setDeviceTargets(next);
    void writeDeviceOverride(next.map((t) => t.agentBackendId));
  };

  // 增删/更换：列表当场跟手，账号级集合随后由 patchExecTargetSet 落库；这一批
  // 写入不带 orderOverride，本端已有的顺序覆盖原样保留（收敛规则会补上新档）。
  const handleExecTargetSetChange = (next: ExecTargetRow[]) => {
    setDeviceTargets(next);
    patchExecTargetSet(next);
  };

  // 技能按 backend id 落回账号级集合那一档：行序是本端解析后的顺序，用下标会写错档。
  const patchTargetSkills = (
    agentBackendId: number,
    skills: department_svc.AgentSkillDTO[],
  ) => {
    const next = execTargets.map((target) =>
      target.agentBackendId === agentBackendId ? { ...target, skills } : target,
    );
    patch({ execTargets: next }, { immediate: true });
  };

  const { caps } = useBackendCapabilities(selectedBackend?.type);

  const handleUploadFile = async (file: File) => {
    const dataUrl = await new Promise<string>((resolve, reject) => {
      const reader = new FileReader();
      reader.onload = () => resolve(String(reader.result ?? ""));
      reader.onerror = () => reject(reader.error);
      reader.readAsDataURL(file);
    });
    await wrap(() => props.onUploadAvatar({ id: props.agent.id, dataUrl }));
  };

  const handleDeleteAvatar = async () => {
    await wrap(() => props.onDeleteAvatar({ id: props.agent.id }));
  };

  const handleConfirmDeleteAgent = async () => {
    await props.onDelete({ id: props.agent.id });
    setDeletePromptOpen(false);
  };

  const toolItems = buildToolList(props.availableTools ?? [], tools, t);
  const grantedToolCount = toolItems.filter((it) => it.granted).length;
  const toggleToolGrant = (key: string) =>
    patch(
      {
        tools: tools.map((tl) =>
          tl.key === key ? { ...tl, enabled: !tl.enabled } : tl,
        ),
      },
      { immediate: true },
    );

  const promptCharCount = prompt.replace(/\s/g, "").length;

  // 归属：部门 / 上级 Agent 二选一，实体层的互斥由控件本身表达。
  const parentAgentId = props.agent.parentAgentId ?? 0;
  const placement: Placement =
    parentAgentId > 0
      ? { kind: "agent", id: parentAgentId }
      : { kind: "department", id: props.agent.departmentId ?? 0 };
  const handlePlacementPick = (next: Placement) => {
    void wrap(() =>
      props.onMove(
        agent_svc.MoveAgentRequest.createFrom({
          id: props.agent.id,
          newDepartmentId: next.kind === "department" ? next.id : 0,
          newParentAgentId: next.kind === "agent" ? next.id : 0,
          newSortOrder: 0,
        }),
      ),
    );
  };

  return (
    <div
      data-slot="org-detail-agent"
      className="flex h-full min-w-0 flex-col bg-card"
    >
      <header className="flex shrink-0 items-start gap-3 border-b border-border bg-card px-5 py-4">
        <AgentAvatarPicker
          name={name || props.agent.name}
          avatarColor={avatarColor}
          avatarIcon={avatarIcon}
          avatarDataUrl={props.agent.avatarDataUrl}
          onChangeIcon={(v) => patch({ avatarIcon: v }, { immediate: true })}
          showImageMode={false}
          triggerSize="lg"
        />
        <div className="flex min-w-0 flex-1 flex-col">
          <span className="truncate text-base font-semibold">
            {props.agent.name}
          </span>
          {props.isLeadOf && (
            <span className="truncate font-mono text-2xs text-primary-text">
              {t("org.agent.departmentLead", { name: props.isLeadOf.name })}
            </span>
          )}
        </div>
        <div className="flex shrink-0 gap-1">
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            disabled={isCEO}
            aria-label={t("org.agent.actions.deleteAgent")}
            onClick={() => setDeletePromptOpen(true)}
          >
            <Trash2 className="size-4 text-destructive" />
          </Button>
          <Button
            variant="outline"
            size="icon"
            className="size-8"
            aria-label={t("common.close")}
            onClick={props.onClose}
          >
            <X className="size-4" />
          </Button>
        </div>
      </header>

      {/* 三栏按**容器**宽度分档，不按视口：详情是主区里的一块，视口宽不等于它宽。
          每一栏都 min-w-0 —— 网格项默认 min-width:auto，一串不可断的提示词/路径
          会顶着栏宽不肯收缩，外层滚动容器再把溢出藏起来，谁都不报错。 */}
      <div className="@container min-h-0 min-w-0 flex-1 overflow-y-auto px-5 py-5">
        <div
          className="grid min-w-0 grid-cols-1 items-start gap-6 @xl:grid-cols-2 @3xl:grid-cols-3"
          data-slot="org-detail-columns"
        >
          <section
            aria-label={t("org.detail.columns.identity")}
            data-slot="org-detail-col-identity"
            className="flex min-w-0 flex-col gap-4"
          >
            <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
              {t("org.detail.columns.identity")}
            </h3>
            <div className="space-y-1.5">
              <label className="block text-2xs text-muted-foreground">
                {t("org.department.name")}
              </label>
              <Input
                value={name}
                onChange={(e) => patch({ name: e.target.value })}
                onBlur={flush}
                aria-label={t("org.department.name")}
              />
            </div>
            <div className="space-y-1.5">
              <div className="flex items-center justify-between gap-2">
                <label className="text-2xs text-muted-foreground">
                  {t("org.department.description")}
                </label>
                <span className="font-mono text-2xs text-muted-foreground">
                  {t("org.agent.descriptionHint")}
                </span>
              </div>
              <Input
                value={description}
                onChange={(e) => patch({ description: e.target.value })}
                onBlur={flush}
                aria-label={t("org.department.description")}
              />
            </div>
            <div className="space-y-2">
              <label className="block text-2xs text-muted-foreground">
                {t("org.chart.newAgent.avatar")}
              </label>
              <div className="flex min-w-0 items-center gap-3">
                <AgentAvatarPicker
                  name={name || props.agent.name}
                  avatarColor={avatarColor}
                  avatarIcon={avatarIcon}
                  avatarDataUrl={props.agent.avatarDataUrl}
                  onChangeIcon={(v) =>
                    patch({ avatarIcon: v }, { immediate: true })
                  }
                  showImageMode={false}
                  triggerSize="lg"
                  triggerClassName="size-12 rounded-lg"
                />
                <AgentAvatarUploadActions
                  avatarDataUrl={props.agent.avatarDataUrl}
                  onUpload={handleUploadFile}
                  onDelete={handleDeleteAvatar}
                  uploadLabel={
                    props.agent.avatarDataUrl
                      ? t("org.agent.avatar.replaceImage")
                      : t("org.agent.avatar.uploadImage")
                  }
                />
              </div>
            </div>
            <div className="space-y-2">
              <label className="block text-2xs text-muted-foreground">
                {t("org.chart.newAgent.avatarColor")}
              </label>
              <div
                className="grid grid-cols-5 gap-2"
                role="radiogroup"
                aria-label={t("org.chart.newAgent.avatarColor")}
              >
                {agentColorOrder.map((c) => (
                  <button
                    key={c}
                    type="button"
                    role="radio"
                    aria-checked={avatarColor === c}
                    aria-label={t("org.chart.newAgent.avatarColorNamed", {
                      color: c,
                    })}
                    onClick={() =>
                      patch(
                        { avatarColor: c as AgentColor },
                        { immediate: true },
                      )
                    }
                    className={cn(
                      "size-7 rounded-full ring-offset-2 transition-all",
                      agentColorClassNames[c],
                      avatarColor === c && "ring-2 ring-primary",
                    )}
                  />
                ))}
              </div>
            </div>
            <PlacementField
              agent={props.agent}
              agents={props.agents}
              departments={props.departments}
              placement={placement}
              reportTarget={reportTarget}
              onPick={handlePlacementPick}
            />
          </section>

          <section
            aria-label={t("org.detail.columns.behavior")}
            data-slot="org-detail-col-behavior"
            className="flex min-w-0 flex-col gap-4"
          >
            <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
              {t("org.detail.columns.behavior")}
            </h3>
            <div className="min-w-0 space-y-2" data-slot="agent-section-prompt">
              <div className="flex items-center justify-between gap-2">
                <label className="font-mono text-2xs text-muted-foreground">
                  {t("org.agent.systemPrompt")}
                </label>
                <span className="font-mono text-2xs text-muted-foreground">
                  {t("org.agent.charCount", { count: promptCharCount })}
                </span>
              </div>
              <Textarea
                value={prompt}
                onChange={(e) => patch({ prompt: e.target.value })}
                onBlur={flush}
                aria-label={t("org.agent.systemPrompt")}
                className="min-h-[160px] w-full font-mono text-xs"
              />
              <div className="flex items-start gap-1.5 text-2xs text-muted-foreground">
                <Info className="mt-px size-3 shrink-0" aria-hidden="true" />
                <span className="min-w-0">
                  {t("org.agent.systemPromptHint")}
                </span>
              </div>
            </div>

            {caps?.has("mcp_tools") && (
              <div
                className="min-w-0 space-y-2"
                data-slot="agent-section-tools"
              >
                <div className="flex items-center gap-2">
                  <h4 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
                    {t("org.agent.tools.sectionTitle")}
                  </h4>
                  <div className="flex-1" />
                  <span className="font-mono text-2xs text-muted-foreground">
                    {t("org.agent.tools.enabledCount", {
                      count: grantedToolCount,
                    })}
                  </span>
                </div>
                {toolItems.length === 0 ? (
                  <p className="text-2xs text-muted-foreground">
                    {t("org.agent.tools.empty")}
                  </p>
                ) : (
                  <ul
                    aria-label={t("org.agent.tools.title")}
                    className="flex min-w-0 flex-col overflow-hidden rounded-md border border-border"
                  >
                    {toolItems.map((item, index) => {
                      const Icon = TOOL_ICONS[item.key] ?? Wrench;
                      return (
                        <li
                          key={item.key}
                          data-granted={item.granted}
                          className={cn(
                            "flex min-w-0 items-start gap-2 px-2.5 py-2",
                            index > 0 && "border-t border-border",
                            item.granted ? "bg-primary-soft" : "bg-input-bg",
                          )}
                        >
                          <span
                            className={cn(
                              "mt-0.5 shrink-0",
                              item.granted
                                ? "text-primary-text"
                                : "text-muted-foreground",
                            )}
                            aria-hidden="true"
                          >
                            {item.granted ? (
                              <Check className="size-3.5" />
                            ) : (
                              <Icon className="size-3.5" />
                            )}
                          </span>
                          <div className="flex min-w-0 flex-1 flex-col gap-0.5">
                            <span className="flex min-w-0 flex-wrap items-center gap-1.5">
                              <span className="truncate text-xs font-semibold">
                                {item.name}
                              </span>
                              {item.approval && (
                                <span className="inline-flex shrink-0 items-center rounded-sm bg-status-waiting-bg px-1.5 py-0.5 font-mono text-2xs text-status-waiting">
                                  {t("org.agent.tools.approval")}
                                </span>
                              )}
                            </span>
                            <span className="min-w-0 break-words text-2xs text-muted-foreground">
                              {item.description}
                            </span>
                          </div>
                          <button
                            type="button"
                            aria-label={t(
                              item.granted
                                ? "org.agent.tools.revokeNamed"
                                : "org.agent.tools.grantNamed",
                              { name: item.name },
                            )}
                            title={t(
                              item.granted
                                ? "org.agent.tools.revokeNamed"
                                : "org.agent.tools.grantNamed",
                              { name: item.name },
                            )}
                            onClick={() => toggleToolGrant(item.key)}
                            className="mt-0.5 shrink-0 rounded-sm text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
                          >
                            {item.granted ? (
                              <X className="size-3.5" />
                            ) : (
                              <Plus className="size-3.5" />
                            )}
                          </button>
                        </li>
                      );
                    })}
                  </ul>
                )}
              </div>
            )}
          </section>

          <section
            aria-label={t("org.detail.columns.execution")}
            data-slot="org-detail-col-execution"
            className="flex min-w-0 flex-col gap-4"
          >
            <h3 className="font-mono text-2xs font-semibold uppercase tracking-wide text-muted-foreground">
              {t("org.detail.columns.execution")}
            </h3>
            {builtinMissingProvider ? (
              <Alert
                className="border-status-waiting/40 bg-status-waiting-bg text-xs"
                data-testid="org-agent-provider-gap"
              >
                <AlertTriangle className="size-4" aria-hidden="true" />
                <AlertTitle className="text-xs">
                  {t("org.agent.backend.providerGapTitle")}
                </AlertTitle>
                <AlertDescription className="text-2xs">
                  {t("org.agent.backend.providerGapDescription")}
                  <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1">
                    <Button
                      type="button"
                      size="sm"
                      className="h-7 px-2.5 text-2xs"
                      onClick={() =>
                        navigate("/settings", {
                          state: { settingsPage: "llm-providers" },
                        })
                      }
                    >
                      {t("org.agent.backend.configureProvider")}
                    </Button>
                    <Button
                      type="button"
                      variant="link"
                      size="sm"
                      className="h-auto px-0 text-2xs"
                      onClick={() =>
                        navigate("/settings", {
                          state: { settingsPage: "agent-backend" },
                        })
                      }
                    >
                      {t("org.agent.backend.goAgentBackendSettings")}
                    </Button>
                  </div>
                </AlertDescription>
              </Alert>
            ) : null}
            {/* 执行目标区只有这一个列表：它恒等于这台电脑当前实际的派发顺序，
                每一档的技能折在它自己那一行里。 */}
            <ExecTargetList
              agentId={props.agent.id}
              agentName={props.agent.name}
              availability={availability.byBackendId}
              targets={listTargets}
              backends={backendsForList}
              onChange={handleExecTargetSetChange}
              onReorder={handleExecTargetReorder}
              onSkillsChange={patchTargetSkills}
              loading={orderPending}
              saveRejected={pendingInvalid && execTargets.length === 0}
            />
            {/* 技能三种色调的图例只出一次：芯片本身折在各行里，逐行重复一遍图例
                比不给还吵。 */}
            {execTargets.length > 0 && (
              <p className="font-mono text-2xs text-muted-foreground">
                {t("org.agent.skills.inheritNote")}
              </p>
            )}
          </section>
        </div>
      </div>

      <AutoSaveStatus
        status={status}
        pendingInvalid={pendingInvalid}
        onRetry={retry}
      />

      <Dialog
        open={deletePromptOpen}
        onOpenChange={(o) => !o && setDeletePromptOpen(false)}
      >
        {deletePromptOpen && (
          <DialogContent className="max-w-md">
            <DialogHeader>
              <DialogTitle className="flex items-center gap-2">
                <AlertTriangle
                  className="size-[18px] text-destructive"
                  aria-hidden="true"
                />
                <span>
                  {t("org.agent.deleteDialog.title", {
                    name: props.agent.name,
                  })}
                </span>
              </DialogTitle>
              <DialogDescription>
                {t("org.agent.deleteDialog.description")}
              </DialogDescription>
            </DialogHeader>
            <DialogFooter>
              <span className="mr-auto font-mono text-2xs text-muted-foreground">
                {t("org.department.deleteDialog.irreversible")}
              </span>
              <Button
                variant="outline"
                size="sm"
                onClick={() => setDeletePromptOpen(false)}
              >
                {t("common.cancel")}
              </Button>
              <Button
                variant="destructive"
                size="sm"
                onClick={() => void handleConfirmDeleteAgent()}
              >
                <Trash2 className="size-3.5" />
                {t("org.department.deleteDialog.confirm")}
              </Button>
            </DialogFooter>
          </DialogContent>
        )}
      </Dialog>
    </div>
  );
}

type Placement =
  | { kind: "department"; id: number }
  | { kind: "agent"; id: number };

type PlacementOption = {
  kind: "department" | "agent";
  id: number;
  label: string;
  disabledReason?: string;
  agent?: OrgAgent;
  department?: OrgDepartment;
};

// PlacementField 是「归属」那一格：一个下拉、两组互斥选项，外加部门归属时那条
// 只读的推导行。移动端改归属全靠它（索引里系统 Agent 行不接受落点，拖拽到不了
// 顶层），所以它必须自足：能搜、能看清为什么某一项不能选。
function PlacementField(props: {
  agent: OrgAgent;
  agents: OrgAgent[];
  departments: OrgDepartment[];
  placement: Placement;
  reportTarget: OrgAgent | null;
  onPick: (next: Placement) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const isCEO = props.agent.systemBadge === SYSTEM_BADGE;
  const departmentHeadingId = React.useId();
  const agentHeadingId = React.useId();

  const departmentById = React.useMemo(
    () => new Map(props.departments.map((d) => [d.id, d])),
    [props.departments],
  );

  // 子部门带上祖先路径：同名部门（「平台组」在两个事业部下各有一个）不带路径就是
  // 两行一模一样的选项。
  const departmentLabel = React.useCallback(
    (department: OrgDepartment): string => {
      const names: string[] = [department.name];
      const seen = new Set<number>([department.id]);
      let parentId = department.parentId ?? 0;
      while (parentId > 0 && !seen.has(parentId)) {
        const parent = departmentById.get(parentId);
        if (!parent) break;
        names.unshift(parent.name);
        seen.add(parent.id);
        parentId = parent.parentId ?? 0;
      }
      return names.join(" / ");
    },
    [departmentById],
  );

  // 置灰的判据与拖拽的非法落点是同一条：isValidOrgDrop 里的成环规则与服务端
  // agent_svc.hasAgentCycle 逐字同规则。这里唯一不同的是系统 Agent —— 索引里它
  // 不接受落点（挂上去读起来像顶层），而「回到顶层」正是要挂到它下面，只能走这个
  // 下拉，所以对它单独放行，不再问落点判定。
  const dropContext = React.useMemo(() => {
    const agents = props.agents.some((a) => a.id === props.agent.id)
      ? props.agents
      : [...props.agents, props.agent];
    return { agents, departments: props.departments };
  }, [props.agents, props.agent, props.departments]);

  const options: { departments: PlacementOption[]; agents: PlacementOption[] } =
    React.useMemo(() => {
      const q = query.trim().toLowerCase();
      const match = (label: string) => !q || label.toLowerCase().includes(q);
      const departments = props.departments
        .map((department) => ({
          kind: "department" as const,
          id: department.id,
          label: departmentLabel(department),
          department,
        }))
        .filter((option) => match(option.label));
      const agents = props.agents
        .map((agent) => {
          const isSelf = agent.id === props.agent.id;
          const allowed =
            !isSelf &&
            (agent.systemBadge === SYSTEM_BADGE ||
              isValidOrgDrop(
                props.agent.id,
                { kind: "agent", agentId: agent.id },
                dropContext,
              ));
          return {
            kind: "agent" as const,
            id: agent.id,
            label: agent.name,
            agent,
            disabledReason: allowed
              ? undefined
              : isSelf
                ? t("org.agent.placement.reasonSelf")
                : t("org.agent.placement.reasonCycle"),
          };
        })
        .filter((option) => match(option.label));
      return { departments, agents };
    }, [
      props.departments,
      props.agents,
      props.agent.id,
      departmentLabel,
      dropContext,
      query,
      t,
    ]);

  const selectedDepartment =
    props.placement.kind === "department"
      ? (departmentById.get(props.placement.id) ?? null)
      : null;
  const selectedAgent =
    props.placement.kind === "agent"
      ? (props.agents.find((a) => a.id === props.placement.id) ?? null)
      : null;

  const pick = (option: PlacementOption) => {
    if (option.disabledReason) return;
    setOpen(false);
    setQuery("");
    props.onPick({ kind: option.kind, id: option.id });
  };

  const renderOption = (option: PlacementOption) => {
    const selected =
      props.placement.kind === option.kind && props.placement.id === option.id;
    return (
      <button
        key={`${option.kind}-${option.id}`}
        type="button"
        role="option"
        aria-selected={selected}
        aria-disabled={option.disabledReason ? true : undefined}
        onClick={() => pick(option)}
        className={cn(
          "flex w-full min-w-0 items-center gap-2 px-2.5 py-1.5 text-left",
          option.disabledReason
            ? "cursor-not-allowed opacity-50"
            : "hover:bg-accent",
          selected && "bg-accent",
        )}
      >
        {option.agent ? (
          <AgentAvatar
            name={option.agent.name}
            color={safeAgentColor(option.agent.avatarColor)}
            avatarDataUrl={option.agent.avatarDataUrl}
            avatarIcon={option.agent.avatarIcon}
            className="size-5 shrink-0 rounded-sm text-2xs"
          />
        ) : (
          <DepartmentGlyph department={option.department} />
        )}
        <span className="min-w-0 flex-1 truncate text-xs">{option.label}</span>
        {option.disabledReason && (
          <span className="shrink-0 font-mono text-2xs text-muted-foreground">
            {option.disabledReason}
          </span>
        )}
      </button>
    );
  };

  return (
    <div className="min-w-0 space-y-1.5" data-slot="agent-section-placement">
      <label className="block text-2xs text-muted-foreground">
        {t("org.agent.placement.label")}
      </label>
      <Popover
        open={open}
        onOpenChange={(next) => {
          setOpen(next);
          if (!next) setQuery("");
        }}
      >
        <PopoverTrigger asChild>
          <button
            type="button"
            role="combobox"
            aria-expanded={open}
            aria-label={t("org.agent.placement.label")}
            disabled={isCEO}
            className={cn(
              "flex w-full min-w-0 items-center gap-2 rounded-md border border-border bg-input-bg px-2.5 py-2 text-left",
              isCEO ? "cursor-not-allowed opacity-60" : "hover:bg-accent",
            )}
          >
            {selectedAgent ? (
              <AgentAvatar
                name={selectedAgent.name}
                color={safeAgentColor(selectedAgent.avatarColor)}
                avatarDataUrl={selectedAgent.avatarDataUrl}
                avatarIcon={selectedAgent.avatarIcon}
                className="size-5 shrink-0 rounded-sm text-2xs"
              />
            ) : (
              <DepartmentGlyph department={selectedDepartment ?? undefined} />
            )}
            <span className="min-w-0 flex-1 truncate text-xs">
              {selectedAgent
                ? selectedAgent.name
                : selectedDepartment
                  ? departmentLabel(selectedDepartment)
                  : t("org.department.topLevel")}
            </span>
          </button>
        </PopoverTrigger>
        <PopoverContent className="w-72 p-0" align="start">
          <div className="flex items-center gap-1.5 border-b border-border px-2.5 py-2">
            <Search
              className="size-3.5 shrink-0 text-muted-foreground"
              aria-hidden="true"
            />
            <input
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={t("org.agent.placement.searchPlaceholder")}
              aria-label={t("org.agent.placement.searchAria")}
              className="w-full min-w-0 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
            />
          </div>
          <div
            role="listbox"
            aria-label={t("org.agent.placement.label")}
            className="max-h-72 overflow-y-auto py-1"
          >
            {options.departments.length === 0 &&
              options.agents.length === 0 && (
                <p className="px-2.5 py-2 text-2xs text-muted-foreground">
                  {t("org.agent.placement.noMatch")}
                </p>
              )}
            {options.departments.length > 0 && (
              <div role="group" aria-labelledby={departmentHeadingId}>
                <div
                  id={departmentHeadingId}
                  className="px-2.5 pb-1 pt-1.5 font-mono text-2xs font-semibold text-subtle-foreground"
                >
                  {t("org.agent.placement.groupDepartment")}
                </div>
                {options.departments.map(renderOption)}
              </div>
            )}
            {options.agents.length > 0 && (
              <div role="group" aria-labelledby={agentHeadingId}>
                <div
                  id={agentHeadingId}
                  className="px-2.5 pb-1 pt-1.5 font-mono text-2xs font-semibold text-subtle-foreground"
                >
                  {t("org.agent.placement.groupParent")}
                </div>
                {options.agents.map(renderOption)}
              </div>
            )}
          </div>
        </PopoverContent>
      </Popover>
      {isCEO && (
        <p className="font-mono text-2xs text-muted-foreground">
          {t("org.agent.placement.systemLocked")}
        </p>
      )}
      {/* 归属是部门时汇报对象是推导出来的（部门负责人），所以要说明它随负责人变动；
          归属是上级 Agent 时汇报对象就等于归属本身，再说一遍是复述。 */}
      {!isCEO &&
        props.placement.kind === "department" &&
        props.reportTarget && (
          <div
            className="flex min-w-0 flex-wrap items-center gap-1.5 text-2xs text-muted-foreground"
            data-testid="org-agent-derived-manager"
          >
            <CornerDownRight className="size-3 shrink-0" aria-hidden="true" />
            <span>{t("org.agent.reportsTo")}</span>
            <span className="inline-flex min-w-0 items-center gap-1.5 rounded-sm bg-secondary px-1.5 py-0.5 font-mono text-foreground">
              <AgentAvatar
                name={props.reportTarget.name}
                color={safeAgentColor(props.reportTarget.avatarColor)}
                avatarDataUrl={props.reportTarget.avatarDataUrl}
                avatarIcon={props.reportTarget.avatarIcon}
                className="size-5 rounded-sm text-2xs"
              />
              <span className="truncate">{props.reportTarget.name}</span>
            </span>
            <span className="opacity-60">
              {t("org.agent.placement.derivedManager")}
            </span>
          </div>
        )}
    </div>
  );
}

function DepartmentGlyph({ department }: { department?: OrgDepartment }) {
  const Icon = iconForKey(department?.icon ?? "puzzle");
  return (
    <span
      className={cn(
        "inline-flex size-5 shrink-0 items-center justify-center rounded-sm text-white",
        agentColorClassNames[safeAgentColor(department?.accentColor ?? "")],
      )}
      aria-hidden="true"
    >
      {React.createElement(Icon, { className: "size-3" })}
    </span>
  );
}

// backendItemFromSummary 把 AgentItem.backend（department_svc.BackendSummary，只有
// 少数字段的只读摘要）撑成 ExecTargetList/ExecTargetSkillsBlock 期望的
// agent_backend_svc.BackendItem 形状——只在 props.backends 全量列表还没覆盖这个
// backend 时才用得到这份兜底，撑出来的字段（deviceId 等）留空/假值即可：这个兜底
// 场景下这个 backend 本来就只知道这几个摘要字段。
function backendItemFromSummary(
  b: department_svc.BackendSummary,
): agent_backend_svc.BackendItem {
  return {
    id: b.id,
    type: b.type,
    name: b.name,
    llmProviderKey: "",
    llmProviderName: b.llmProviderName,
    llmProviderType: "",
    llmProviderModel: b.llmProviderModel,
    llmProviderActive: b.llmProviderActive,
    llmModelKey: "",
    cliPath: "",
    modelRoutes: {},
    sandbox: "",
    approval: "",
    envJson: "",
    reasoningEffort: "",
    defaultPermissionMode: "",
    defaultModel: "",
    openClawGatewayUrl: "",
    openClawAgentId: "",
    openClawDefaultModel: "",
    openClawSessionMode: "",
    hasToken: false,
    deviceId: "",
    deviceName: "",
    online: false,
    agentCount: 0,
    createtime: 0,
    updatetime: 0,
  } as unknown as agent_backend_svc.BackendItem;
}
