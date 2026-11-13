// 记忆页的归属目录：左列、「移到…」、搜索结果的组名、最近变更里的归属标签都读这一份。
//
// 数据来自组织（部门、agent）与项目树两份既有绑定；记忆服务只回 owner 的 kind + id，
// 名字与身份记号由这里补上。纯函数，不认识组件。

import type { MemoryOwnerRef } from "@agentre-hub/agentre-ui";

import type { app, department_svc } from "../../../../wailsjs/go/models";

export type MemoryOwnerMark =
  | { kind: "global" }
  | { kind: "department"; icon: string; accentColor: string }
  | {
      kind: "agent";
      color: string;
      avatarIcon: string;
      avatarDataUrl: string;
    }
  | { kind: "project"; icon: string; color: string };

export type MemoryDirectoryEntry = {
  key: string;
  owner: MemoryOwnerRef;
  label: string;
  /** 部门按组织树、项目按项目树的嵌套深度；全局与 agent 恒为 0。 */
  depth: number;
  /** 头部名称下面那一行（agent 的所在部门）。 */
  subtitle?: string;
  mark: MemoryOwnerMark;
};

export type MemoryDirectory = {
  global: MemoryDirectoryEntry;
  departments: MemoryDirectoryEntry[];
  agents: MemoryDirectoryEntry[];
  projects: MemoryDirectoryEntry[];
  byKey: Map<string, MemoryDirectoryEntry>;
};

/**
 * 归属在页面内的唯一键，与路由参数 `owner=` 的取值同形。入参收宽到 Wails 的
 * `memory_entity.Owner`（kind 是 string），后端回来的归属不必先收窄。
 */
export function ownerKey(owner: { kind: string; id: number }): string {
  return owner.kind === "global" ? "global" : `${owner.kind}:${owner.id}`;
}

type Sortable = { id: number; sortOrder: number };

function bySortOrder(a: Sortable, b: Sortable): number {
  return a.sortOrder - b.sortOrder || a.id - b.id;
}

function flattenDepartments(
  departments: department_svc.DepartmentItem[],
): MemoryDirectoryEntry[] {
  const ids = new Set(departments.map((d) => d.id));
  const children = new Map<number, department_svc.DepartmentItem[]>();
  for (const dept of departments) {
    // 上级不在列表里（不该发生）的部门按顶层画，免得整枝消失。
    const parent = ids.has(dept.parentId) ? dept.parentId : 0;
    const list = children.get(parent) ?? [];
    list.push(dept);
    children.set(parent, list);
  }
  const out: MemoryDirectoryEntry[] = [];
  const visit = (parent: number, depth: number) => {
    for (const dept of [...(children.get(parent) ?? [])].sort(bySortOrder)) {
      out.push({
        key: ownerKey({ kind: "department", id: dept.id }),
        owner: { kind: "department", id: dept.id },
        label: dept.name,
        depth,
        mark: {
          kind: "department",
          icon: dept.icon ?? "",
          accentColor: dept.accentColor ?? "",
        },
      });
      visit(dept.id, depth + 1);
    }
  };
  visit(0, 0);
  return out;
}

function flattenProjects(
  nodes: app.ProjectTreeNode[],
  depth = 0,
  out: MemoryDirectoryEntry[] = [],
): MemoryDirectoryEntry[] {
  for (const node of nodes) {
    const project = node.project;
    if (project) {
      out.push({
        key: ownerKey({ kind: "project", id: project.id }),
        owner: { kind: "project", id: project.id },
        label: project.name,
        depth,
        mark: {
          kind: "project",
          icon: project.icon ?? "",
          color: project.color ?? "",
        },
      });
    }
    flattenProjects(node.children ?? [], depth + 1, out);
  }
  return out;
}

export function buildMemoryDirectory(input: {
  globalLabel: string;
  departments: department_svc.DepartmentItem[];
  agents: department_svc.AgentItem[];
  projects: app.ProjectTreeNode[];
}): MemoryDirectory {
  const global: MemoryDirectoryEntry = {
    key: "global",
    owner: { kind: "global", id: 0 },
    label: input.globalLabel,
    depth: 0,
    mark: { kind: "global" },
  };
  const departments = flattenDepartments(input.departments);
  const agents = [...input.agents].sort(bySortOrder).map(
    (agent): MemoryDirectoryEntry => ({
      key: ownerKey({ kind: "agent", id: agent.id }),
      owner: { kind: "agent", id: agent.id },
      label: agent.name,
      depth: 0,
      subtitle: agent.departmentName || undefined,
      mark: {
        kind: "agent",
        color: agent.avatarColor ?? "",
        avatarIcon: agent.avatarIcon ?? "",
        avatarDataUrl: agent.avatarDataUrl ?? "",
      },
    }),
  );
  const projects = flattenProjects(input.projects);
  const byKey = new Map<string, MemoryDirectoryEntry>();
  for (const entry of [global, ...departments, ...agents, ...projects]) {
    byKey.set(entry.key, entry);
  }
  return { global, departments, agents, projects, byKey };
}
