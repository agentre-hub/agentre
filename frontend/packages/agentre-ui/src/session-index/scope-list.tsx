import * as React from "react";
import { Check, ChevronDown } from "lucide-react";

import { splitMatch } from "../board/scope-tree";
import { useUiTranslation } from "../i18n";
import { cn } from "../lib/utils";
import { SearchInput } from "../ui/search-input";
import {
  SegmentedControl,
  SegmentedControlItem,
} from "../ui/segmented-control";
import { AgentAvatar } from "../ui/agent-avatar";

import { ProjectGlyph } from "./project-glyph";

/**
 * 范围清单（规格 2026-11-05「手机外壳」·范围弹层的**内容**）：分段 项目 / Agent /
 * 机器 + 搜索 + 置顶「全部对话」+ 当前档的列表。弹层容器是宿主的事；这里只吃朴素
 * 数据、只吐 `onSelect(scope)`，计数与「等你」由宿主算好带进来。
 */
export type ScopeValue =
  | { kind: "all" }
  | { kind: "project"; id: string }
  | { kind: "agent"; id: string }
  | { kind: "machine"; id: string };

export type ScopeTab = "project" | "agent" | "machine";

type ScopeItemBase = {
  id: string;
  name: string;
  /** 范围内的对话数。 */
  count: number;
  /** 有事等你：名字旁摆一枚 waiting 色点。 */
  needsYou?: boolean;
};

export type ScopeProjectNode = ScopeItemBase & {
  color?: string;
  icon?: string;
  children?: ScopeProjectNode[];
};

export type ScopeAgentItem = ScopeItemBase & {
  color?: string;
  icon?: string;
  avatarDataUrl?: string;
  online: boolean;
  machineName?: string;
};

export type ScopeAgentGroup = {
  id: string;
  name: string;
  agents: ScopeAgentItem[];
};

export type ScopeMachineItem = ScopeItemBase & { online: boolean };

export type ScopeListProps = {
  /** 「全部对话」的条数。 */
  allCount: number;
  selected: ScopeValue;
  onSelect: (scope: ScopeValue) => void;
  projects: ScopeProjectNode[];
  agentGroups: ScopeAgentGroup[];
  machines: ScopeMachineItem[];
  /** 不传就是非受控，从「项目」档起。 */
  tab?: ScopeTab;
  onTabChange?: (tab: ScopeTab) => void;
  className?: string;
};

const ROW =
  "flex min-h-11 w-full cursor-pointer items-center gap-2.5 rounded-md px-3 text-left text-sm outline-none transition-colors focus-visible:focus-outline motion-reduce:transition-none";

function Highlight({ text, needle }: { text: string; needle: string }) {
  return (
    <>
      {splitMatch(text, needle).map((segment, index) =>
        segment.match ? (
          <mark
            key={index}
            className="rounded-sm bg-primary-soft text-primary-text"
          >
            {segment.text}
          </mark>
        ) : (
          <React.Fragment key={index}>{segment.text}</React.Fragment>
        ),
      )}
    </>
  );
}

type ItemRowProps = {
  testId: string;
  selected: boolean;
  dimmed?: boolean;
  onClick: () => void;
  leading?: React.ReactNode;
  name: string;
  needle: string;
  subtitle?: string;
  count: number;
  needsYou?: boolean;
  className?: string;
};

function ItemRow({
  testId,
  selected,
  dimmed,
  onClick,
  leading,
  name,
  needle,
  subtitle,
  count,
  needsYou,
  className,
}: ItemRowProps) {
  return (
    <button
      type="button"
      data-testid={testId}
      aria-current={selected ? "true" : undefined}
      onClick={onClick}
      className={cn(
        ROW,
        selected
          ? "bg-primary-soft font-medium text-primary-text"
          : "text-foreground hover:bg-field-fill-hover",
        dimmed && "opacity-50",
        className,
      )}
    >
      {leading}
      <span className="flex min-w-0 flex-1 flex-col">
        <span className="truncate">
          <Highlight text={name} needle={needle} />
        </span>
        {subtitle ? (
          <span className="truncate text-xs font-normal text-muted-foreground">
            {subtitle}
          </span>
        ) : null}
      </span>
      {needsYou ? (
        <span
          data-testid="scope-needs-you"
          aria-hidden="true"
          className="size-2 shrink-0 rounded-full bg-status-waiting"
        />
      ) : null}
      <span className="shrink-0 font-mono text-xs text-muted-foreground">
        {count}
      </span>
      {selected ? (
        <Check
          data-testid="scope-check"
          className="size-4 shrink-0"
          aria-hidden="true"
        />
      ) : null}
    </button>
  );
}

/** 搜索时只留命中项与其祖先；祖先本身没命中也留着，树才读得通。 */
function filterProjects(
  nodes: ScopeProjectNode[],
  needle: string,
): ScopeProjectNode[] {
  const key = needle.trim().toLowerCase();
  if (!key) return nodes;
  return nodes.flatMap((node) => {
    const children = filterProjects(node.children ?? [], needle);
    if (node.name.toLowerCase().includes(key) || children.length > 0) {
      return [{ ...node, children }];
    }
    return [];
  });
}

function matches(name: string, needle: string) {
  const key = needle.trim().toLowerCase();
  return !key || name.toLowerCase().includes(key);
}

function ProjectTree({
  nodes,
  depth,
  needle,
  selected,
  collapsed,
  onToggle,
  onSelect,
}: {
  nodes: ScopeProjectNode[];
  depth: number;
  needle: string;
  selected: ScopeValue;
  collapsed: ReadonlySet<string>;
  onToggle: (id: string) => void;
  onSelect: (scope: ScopeValue) => void;
}) {
  const { t } = useUiTranslation();
  return (
    <>
      {nodes.map((node) => {
        const hasChildren = (node.children?.length ?? 0) > 0;
        // 搜索中一律展开：命中项藏在折叠里等于没搜到。
        const open = !!needle.trim() || !collapsed.has(node.id);
        return (
          <li key={node.id} className="list-none">
            <div className="flex items-center">
              {hasChildren ? (
                <button
                  type="button"
                  aria-label={t("sessionIndex.scope.toggle", {
                    name: node.name,
                  })}
                  aria-expanded={open}
                  onClick={() => onToggle(node.id)}
                  className="flex size-11 shrink-0 cursor-pointer items-center justify-center rounded-md text-muted-foreground outline-none hover:bg-field-fill-hover focus-visible:focus-outline"
                  style={{ marginLeft: depth * 16 }}
                >
                  <ChevronDown
                    className={cn(
                      "size-4 transition-transform motion-reduce:transition-none",
                      !open && "-rotate-90",
                    )}
                    aria-hidden="true"
                  />
                </button>
              ) : (
                <span
                  className="size-11 shrink-0"
                  style={{ marginLeft: depth * 16 }}
                  aria-hidden="true"
                />
              )}
              <ItemRow
                testId={`scope-item-project-${node.id}`}
                selected={
                  selected.kind === "project" && selected.id === node.id
                }
                onClick={() => onSelect({ kind: "project", id: node.id })}
                leading={
                  <ProjectGlyph
                    project={{
                      name: node.name,
                      color: node.color,
                      icon: node.icon,
                    }}
                    className="size-5 rounded-sm"
                  />
                }
                name={node.name}
                needle={needle}
                count={node.count}
                needsYou={node.needsYou}
                className="min-w-0 flex-1"
              />
            </div>
            {hasChildren && open ? (
              <ul>
                <ProjectTree
                  nodes={node.children ?? []}
                  depth={depth + 1}
                  needle={needle}
                  selected={selected}
                  collapsed={collapsed}
                  onToggle={onToggle}
                  onSelect={onSelect}
                />
              </ul>
            ) : null}
          </li>
        );
      })}
    </>
  );
}

function SectionLabel({ children }: { children: React.ReactNode }) {
  return (
    <div className="px-3 pt-3 pb-1 text-xs font-medium text-muted-foreground">
      {children}
    </div>
  );
}

export function ScopeList({
  allCount,
  selected,
  onSelect,
  projects,
  agentGroups,
  machines,
  tab: tabProp,
  onTabChange,
  className,
}: ScopeListProps) {
  const { t } = useUiTranslation();
  const [innerTab, setInnerTab] = React.useState<ScopeTab>("project");
  const [needle, setNeedle] = React.useState("");
  const [collapsed, setCollapsed] = React.useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const tab = tabProp ?? innerTab;

  const toggle = (id: string) =>
    setCollapsed((prev) => {
      const next = new Set(prev);
      if (!next.delete(id)) next.add(id);
      return next;
    });

  const visibleProjects = filterProjects(projects, needle);
  const visibleGroups = agentGroups
    .map((group) => ({
      ...group,
      agents: group.agents.filter((agent) => matches(agent.name, needle)),
    }))
    .filter((group) => group.agents.length > 0);
  const visibleMachines = machines.filter((m) => matches(m.name, needle));
  const empty =
    tab === "project"
      ? visibleProjects.length === 0
      : tab === "agent"
        ? visibleGroups.length === 0
        : visibleMachines.length === 0;

  const machineRow = (machine: ScopeMachineItem) => (
    <li key={machine.id} className="list-none">
      <ItemRow
        testId={`scope-item-machine-${machine.id}`}
        selected={selected.kind === "machine" && selected.id === machine.id}
        dimmed={!machine.online}
        onClick={() => onSelect({ kind: "machine", id: machine.id })}
        leading={
          <span
            data-testid="scope-online"
            data-online={machine.online}
            aria-hidden="true"
            className={cn(
              "size-2 shrink-0 rounded-full",
              machine.online ? "bg-status-running" : "bg-status-idle",
            )}
          />
        }
        name={machine.name}
        needle={needle}
        count={machine.count}
        needsYou={machine.needsYou}
      />
    </li>
  );

  return (
    <div className={cn("flex min-h-0 flex-col gap-2", className)}>
      <SegmentedControl
        value={tab}
        onValueChange={(value) => {
          setInnerTab(value as ScopeTab);
          onTabChange?.(value as ScopeTab);
        }}
        className="w-full"
        aria-label={t("sessionIndex.scope.tabs")}
      >
        {(["project", "agent", "machine"] as const).map((value) => (
          <SegmentedControlItem
            key={value}
            value={value}
            className="h-11 flex-1"
          >
            {t(`sessionIndex.axis.${value}`)}
          </SegmentedControlItem>
        ))}
      </SegmentedControl>
      <SearchInput
        value={needle}
        onChange={setNeedle}
        size="md"
        className="h-11"
        inputClassName="text-base"
        placeholder={t("sessionIndex.scope.search")}
        aria-label={t("sessionIndex.scope.search")}
      />
      <ItemRow
        testId="scope-all"
        selected={selected.kind === "all"}
        onClick={() => onSelect({ kind: "all" })}
        name={t("sessionIndex.scope.all")}
        needle=""
        count={allCount}
      />
      <div className="min-h-0 flex-1 overflow-y-auto">
        {empty ? (
          <p className="px-3 py-6 text-center text-sm text-muted-foreground">
            {t("sessionIndex.scope.empty")}
          </p>
        ) : tab === "project" ? (
          <ul>
            <ProjectTree
              nodes={visibleProjects}
              depth={0}
              needle={needle}
              selected={selected}
              collapsed={collapsed}
              onToggle={toggle}
              onSelect={onSelect}
            />
          </ul>
        ) : tab === "agent" ? (
          visibleGroups.map((group) => (
            <section key={group.id}>
              <SectionLabel>{group.name}</SectionLabel>
              <ul>
                {group.agents.map((agent) => (
                  <li key={agent.id} className="list-none">
                    <ItemRow
                      testId={`scope-item-agent-${agent.id}`}
                      selected={
                        selected.kind === "agent" && selected.id === agent.id
                      }
                      onClick={() => onSelect({ kind: "agent", id: agent.id })}
                      leading={
                        <span className="relative shrink-0">
                          <AgentAvatar
                            name={agent.name}
                            color={agent.color}
                            iconKey={agent.icon}
                            avatarDataUrl={agent.avatarDataUrl}
                            size="sm"
                          />
                          <span
                            data-testid="scope-online"
                            data-online={agent.online}
                            aria-hidden="true"
                            className={cn(
                              "absolute -right-0.5 -bottom-0.5 size-2 rounded-full ring-2 ring-background",
                              agent.online
                                ? "bg-status-running"
                                : "bg-status-idle",
                            )}
                          />
                        </span>
                      }
                      name={agent.name}
                      needle={needle}
                      subtitle={agent.machineName}
                      count={agent.count}
                      needsYou={agent.needsYou}
                    />
                  </li>
                ))}
              </ul>
            </section>
          ))
        ) : (
          [true, false].map((online) => {
            const list = visibleMachines.filter((m) => m.online === online);
            if (list.length === 0) return null;
            return (
              <section key={String(online)}>
                <SectionLabel>
                  {t(
                    online
                      ? "sessionIndex.scope.online"
                      : "sessionIndex.scope.offline",
                  )}
                </SectionLabel>
                <ul>{list.map(machineRow)}</ul>
              </section>
            );
          })
        )}
      </div>
    </div>
  );
}
