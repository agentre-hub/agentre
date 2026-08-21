import * as React from "react";
import { CornerDownRight, Search } from "lucide-react";

import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { NeutralGlyph } from "../session-index/glyph";
import { AgentAvatar } from "../ui/agent-avatar";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { isValidOrgDrop } from "./org-drop";
import {
  isOrgSystemAgent,
  type OrgAgentModel,
  type OrgDepartmentModel,
} from "./types";

/** 归属：部门与上级 Agent 二选一 —— 实体层的互斥由这个取值本身表达。 */
export type OrgPlacement =
  | { kind: "department"; id: number }
  | { kind: "agent"; id: number };

type PlacementOption = {
  kind: "department" | "agent";
  id: number;
  label: string;
  disabledReason?: string;
  agent?: OrgAgentModel;
  department?: OrgDepartmentModel;
};

/** 头像 / 字形由宿主给：身份怎么画是宿主的事（图标注册表 / 自定义头像图片）。 */
export type OrgPlacementFieldProps = {
  agent: OrgAgentModel;
  agents: OrgAgentModel[];
  departments: OrgDepartmentModel[];
  placement: OrgPlacement;
  /** 归属是部门时推导出来的汇报对象；`null` = 不画那条只读行。 */
  reportTarget: OrgAgentModel | null;
  onPick: (next: OrgPlacement) => void;
  renderAgentAvatar?: (
    agent: OrgAgentModel,
    className: string,
  ) => React.ReactNode;
  renderDepartmentGlyph?: (
    department: OrgDepartmentModel | undefined,
  ) => React.ReactNode;
};

// OrgPlacementField 是「归属」那一格：一个下拉、两组互斥选项，外加部门归属时那条
// 只读的推导行。移动端改归属全靠它（索引里系统 Agent 行不接受落点，拖拽到不了
// 顶层），所以它必须自足：能搜、能看清为什么某一项不能选。
export function OrgPlacementField(props: OrgPlacementFieldProps) {
  const { t } = useUiTranslation();
  const [open, setOpen] = React.useState(false);
  const [query, setQuery] = React.useState("");
  const isCEO = isOrgSystemAgent(props.agent);
  const departmentHeadingId = React.useId();
  const agentHeadingId = React.useId();

  const departmentById = React.useMemo(
    () => new Map(props.departments.map((d) => [d.id, d])),
    [props.departments],
  );

  const avatarOf = (agent: OrgAgentModel, className: string) =>
    props.renderAgentAvatar?.(agent, className) ?? (
      <AgentAvatar
        name={agent.name}
        color={agent.avatarColor}
        size="xs"
        className={className}
      />
    );

  const glyphOf = (department: OrgDepartmentModel | undefined) =>
    props.renderDepartmentGlyph?.(department) ?? (
      // 部门那一枚是**色片**不是身份字形：不写字母，也不报名字（部门名就在同一行的
      // 文字里，再报一遍等于读两遍）。部门缺席时连色都没有，退回中性占位。
      department ? (
        <AgentAvatar
          name=""
          initials=""
          color={department.accentColor}
          size="xs"
          className="size-5 shrink-0 rounded-sm"
        />
      ) : (
        <NeutralGlyph className="size-5 shrink-0 rounded-sm" />
      )
    );

  // 子部门带上祖先路径：同名部门（「平台组」在两个事业部下各有一个）不带路径就是
  // 两行一模一样的选项。
  const departmentLabel = React.useCallback(
    (department: OrgDepartmentModel): string => {
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
            (isOrgSystemAgent(agent) ||
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
        {option.agent
          ? avatarOf(option.agent, "size-5 shrink-0 rounded-sm text-2xs")
          : glyphOf(option.department)}
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
            {selectedAgent
              ? avatarOf(selectedAgent, "size-5 shrink-0 rounded-sm text-2xs")
              : glyphOf(selectedDepartment ?? undefined)}
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
                  className="px-2.5 pb-1 pt-1.5 font-mono text-2xs font-semibold text-muted-foreground"
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
                  className="px-2.5 pb-1 pt-1.5 font-mono text-2xs font-semibold text-muted-foreground"
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
              {avatarOf(props.reportTarget, "size-5 rounded-sm text-2xs")}
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
