import * as React from "react";
import { useTranslation } from "react-i18next";

import {
  OrgToolList,
  Textarea,
  type OrgPlacement,
} from "@agentre-hub/agentre-ui";

import {
  agent_svc,
  department_svc,
  type agent_backend_svc,
} from "../../../../wailsjs/go/models";

import { useBackendCapabilities } from "../capability/use-backend-capabilities";
import { resolveReportTo } from "./reporting";
import { safeAgentColor, type OrgAgent, type OrgDepartment } from "./types";
import { useAutoSave } from "./use-auto-save";
import { AutoSaveStatus } from "./auto-save-status";
import { type ExecTargetRow } from "./exec-target-list";
import { useExecTargetOrder } from "./use-exec-target-order";
import {
  DeleteAgentDialog,
  OrgDetailAgentHeader,
} from "./org-detail-agent-chrome";
import { OrgDetailAgentIdentity } from "./org-detail-agent-identity";
import { OrgDetailAgentExecution } from "./org-detail-agent-execution";

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

function execTargetsFromAgent(agent: OrgAgent): ExecTargetEdit[] {
  return (agent.execTargets ?? []).map((t) => ({
    agentBackendId: t.agentBackendId,
    skills: (t.skills ?? []).map((s) => ({ ...s })),
  }));
}

export function OrgDetailAgent(props: Props) {
  const { t } = useTranslation();
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

  const { availability, setDeviceTargets, orderPending, listTargets } =
    useExecTargetOrder(props.agent.id, execTargets);

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
  const placement: OrgPlacement =
    parentAgentId > 0
      ? { kind: "agent", id: parentAgentId }
      : { kind: "department", id: props.agent.departmentId ?? 0 };
  const handlePlacementPick = (next: OrgPlacement) => {
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
      <OrgDetailAgentHeader
        agent={props.agent}
        name={name}
        avatarColor={avatarColor}
        avatarIcon={avatarIcon}
        isLeadOf={props.isLeadOf}
        isCEO={isCEO}
        onChangeIcon={(v) => patch({ avatarIcon: v }, { immediate: true })}
        onDelete={() => setDeletePromptOpen(true)}
        onClose={props.onClose}
      />

      {/* 三栏按**容器**宽度分档，不按视口：详情是主区里的一块，视口宽不等于它宽。
          每一栏都 min-w-0 —— 网格项默认 min-width:auto，一串不可断的提示词/路径
          会顶着栏宽不肯收缩，外层滚动容器再把溢出藏起来，谁都不报错。 */}
      <div className="@container min-h-0 min-w-0 flex-1 overflow-y-auto px-5 py-5">
        <div
          className="grid min-w-0 grid-cols-1 items-start gap-6 @xl:grid-cols-2 @3xl:grid-cols-3"
          data-slot="org-detail-columns"
        >
          <OrgDetailAgentIdentity
            agent={props.agent}
            agents={props.agents}
            departments={props.departments}
            name={name}
            description={description}
            avatarColor={avatarColor}
            avatarIcon={avatarIcon}
            patch={patch}
            flush={flush}
            placement={placement}
            reportTarget={reportTarget}
            onPlacementPick={handlePlacementPick}
            onUploadFile={handleUploadFile}
            onDeleteAvatar={handleDeleteAvatar}
          />

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
            </div>

            {caps?.has("mcp_tools") && (
              <OrgToolList
                toolKeys={props.availableTools ?? []}
                agentTools={tools}
                onToggleGrant={toggleToolGrant}
              />
            )}
          </section>

          <OrgDetailAgentExecution
            agentId={props.agent.id}
            agentName={props.agent.name}
            availability={availability.byBackendId}
            targets={listTargets}
            backends={backendsForList}
            builtinMissingProvider={builtinMissingProvider}
            hasExecTargets={execTargets.length > 0}
            loading={orderPending}
            saveRejected={pendingInvalid && execTargets.length === 0}
            onChange={handleExecTargetSetChange}
            onReorder={handleExecTargetReorder}
            onSkillsChange={patchTargetSkills}
          />
        </div>
      </div>

      <AutoSaveStatus
        status={status}
        pendingInvalid={pendingInvalid}
        onRetry={retry}
      />

      <DeleteAgentDialog
        open={deletePromptOpen}
        agentName={props.agent.name}
        onOpenChange={(o) => !o && setDeletePromptOpen(false)}
        onCancel={() => setDeletePromptOpen(false)}
        onConfirm={() => void handleConfirmDeleteAgent()}
      />
    </div>
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
