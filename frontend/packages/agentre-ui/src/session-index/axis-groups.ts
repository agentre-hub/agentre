/**
 * 会话索引的轴投影（规格 2026-08-18「共享包承载什么」）。
 *
 * 纯函数一层：进去的是「账号里的会话 + 项目树 + Agent 名单 + 机器名单」，出来的是
 * 有序的组。渲染、筛选、选中都不在这里——那样这一层才测得动，也才不用为了试一个
 * 分组顺序去挂一棵 React 树。
 *
 * 实现由 agentre-server 的 `frontend/src/lib/sessionAxes.ts` 迁入，**两端共用**：
 * 组怎么分、怎么排、兜底组摆在哪，只该有一份答案。
 *
 * 四个轴的分组键：项目同步标识（按 parentSyncId 递归成树）/ agentSyncId / 不分组按
 * 最后活动时间倒序 / 会话所在设备。三个兜底组各自独立、不合并成一个「其他」
 * （决策 7），且在各自的轴上排最后。
 *
 * **可选轴清单由宿主传入**（决策 17）：桌面端今天只 offer 项目 / Agent / 时间三档，
 * server 控制台四档全给。所以这里只有轴的**词汇表**，没有「选择器里摆几档」——
 * 那一条各端自己说了算（见 `AxisPicker` 的 `axes`）。
 *
 * 组头只代表「这里真有东西」（决策 10）：四个轴都只摆有会话的组，因此一条会话
 * 都没有时这里交白卷，由宿主的空态承接。项目轴的祖先组是例外，见 projectGroups。
 *
 * 每一行都带齐三维（决策 8）：分组说了哪一维，行首字形与第二行就补另外两维。
 * 补齐在这里做，调用方直接拿 row.agent / row.project / row.machine 渲染。
 */

export type IndexAxis = "project" | "agent" | "time" | "machine";

/** 兜底组的键。跟真实的项目 / Agent 同步标识撞不上（那些不带下划线包边）。 */
export const UNASSIGNED_PROJECT_KEY = "__unassigned_project__";
export const UNNAMED_AGENT_KEY = "__unnamed_agent__";
export const UNKNOWN_MACHINE_KEY = "__unknown_machine__";

/**
 * 索引里的一行。行的身份是**（发起端指纹, 那一端的会话标识）**，不是此刻承载它的
 * 那台机器——同一条对话在桌面端与 agentred 上各有一份副本是常态，用承载机器做身份
 * 会让它在索引里出现两次。
 */
export interface IndexRow {
  key: string;
  sessionId: number;
  /**
   * 发起端那台机器在账号里的设备标识。**可能没有**：浏览器发起的对话，发起端指纹
   * 就是浏览器自己的指纹，它不在设备名单里。认不出机器不影响读——本体在 server
   * 上，所以这样的行照常列出来，只是机器那一维空着。
   */
  deviceId?: number;
  /** 发起端指纹。它与 sessionId 一起构成这条对话在账号里的身份。 */
  fingerprint: string;
  /** 空串 = 没有 Agent 标识的老会话。 */
  agentSyncId: string;
  /** 空串 = 判不出这条会话归哪个项目（cwd 配不上任何项目路径）。 */
  projectSyncId: string;
  /** 最后活动时间；缺这一列的老会话是 0，时间轴上排在最后。 */
  updatedAt: number;
  /**
   * 行标题。老会话的退化标题（「工作目录 · 后端 · 状态」）由宿主先算好再进来：
   * 那是一条文案，要 i18n，而这一层是纯函数、不认识 t()。
   */
  title: string;
  lifecycleState: string;
  waitingForInput?: boolean;
  /**
   * 这一条在不在账号里（= 有没有被保存过）。机器轴选中一台在线机器时索引会额外
   * 列出那台机器上有、账号里还没有的对话（决策 11），行尾的「保存」据此出现。
   */
  saved?: boolean;
  /**
   * 标题之外还能被搜到的字段（后端 / 机器名 / 组头文案）。放在行上而不是让这一层
   * 去认识会话摘要：投影只排序分组，搜索的口径由宿主定。
   */
  searchFields?: Array<string | undefined>;
}

export interface ProjectNode {
  syncId: string;
  name: string;
  color?: string;
  /**
   * 项目自己选的图标（宿主 icon-registry 的 key，例如 "code-xml"）。把 key 换成
   * 图标的那张注册表是宿主的，没有进这个包——要画它就把画好的节点从
   * `ProjectGlyph` 的 `glyph` 插槽递进来，否则字形退回项目名首字。
   */
  icon?: string;
  /** 空 = 根项目。 */
  parentSyncId?: string;
  sortOrder: number;
}

export interface AgentInfo {
  syncId: string;
  name: string;
  color?: string;
}

export interface MachineInfo {
  deviceId: number;
  name: string;
  online: boolean;
}

/** 一行 + 它在三个维度上的归属。当前轴说了的那一维也在，调用方自己挑着不画。 */
export type IndexGroupRow = IndexRow & {
  agent?: AgentInfo;
  project?: ProjectNode;
  machine?: MachineInfo;
};

export type GroupKind =
  | "project"
  | "agent"
  | "machine"
  /** 时间轴的唯一一组：没有组头。 */
  | "all"
  | "unassignedProject"
  | "unnamedAgent";

export interface IndexGroup {
  key: string;
  kind: GroupKind;
  /** 组头文案；时间轴那一组没有组头，为空串。 */
  label: string;
  color?: string;
  /** 项目轴上的树深度（0 = 根），其余轴恒为 0。 */
  depth: number;
  /** 机器轴上这台机器当前不在线。 */
  offline: boolean;
  rows: IndexGroupRow[];
  /**
   * 该组的会话**总数**，用来渲染桌面端的「查看全部 N」。
   *
   * 可选，且投影自己**不数**：它只看得见宿主已经取到的那些行，而分页的宿主
   * （桌面端每个轴一条分页查询）手里才有总数。宿主不给就没有这个字段，也就没有
   * 分页——server 侧一次取全，行为与迁移前逐条相同。
   */
  total?: number;
}

export interface AxisInput {
  rows: IndexRow[];
  projects: ProjectNode[];
  agents: AgentInfo[];
  machines: MachineInfo[];
  /** 兜底组的文案由宿主传（它才有 i18n），不传就用键当兜底文案。 */
  labels?: Partial<
    Record<"unassignedProject" | "unnamedAgent" | "unknownMachine", string>
  >;
  /**
   * 组键 → 该组的会话总数。分页的宿主用它承接「查看全部 N」；键就是 `IndexGroup.key`
   * （项目 / Agent 的同步标识、`device-<id>`、或兜底组那三个常量）。
   * 不传即没有分页。
   */
  totals?: Record<string, number>;
}

function emptyGroup(over: Partial<IndexGroup> & { key: string }): IndexGroup {
  return {
    kind: "project",
    label: "",
    depth: 0,
    offline: false,
    rows: [],
    ...over,
  };
}

/** 把一行补齐成三维都在的行。 */
function enrich(
  row: IndexRow,
  byAgent: Map<string, AgentInfo>,
  byProject: Map<string, ProjectNode>,
  byMachine: Map<number, MachineInfo>,
): IndexGroupRow {
  return {
    ...row,
    agent: byAgent.get(row.agentSyncId),
    project: byProject.get(row.projectSyncId),
    machine:
      row.deviceId === undefined ? undefined : byMachine.get(row.deviceId),
  };
}

/**
 * 项目轴：按 parentSyncId 递归成树，父在子前，depth 供缩进。
 *
 * 一条会话都没有的项目不摆出来，但**祖先要留**：子项目有会话时它的父组是这棵树的
 * 组头，因为「本组自己没有会话」就让它消失，子组会凭空悬着。
 *
 * 反过来，**已经归了项目的行一条都不能丢**：叫不出名字的项目（项目树没取到 / 项目
 * 被删而机器上的路径记录还在）如实自成一组，父项目不在名单里的子项目当根挂——
 * 否则这些行会从索引里人间蒸发，而它们在别的轴上还好端端地在。
 */
function projectGroups(
  rows: IndexGroupRow[],
  projects: ProjectNode[],
): IndexGroup[] {
  const byId = new Map(projects.map((p) => [p.syncId, p]));
  // 名单外但被会话引用到的标识如实自成一组，名字就用标识本身，不猜——与 Agent 轴
  // 同一条规则。排序键取最大，这类组因此沉在真项目之后。
  for (const row of rows) {
    if (row.projectSyncId && !byId.has(row.projectSyncId)) {
      byId.set(row.projectSyncId, {
        syncId: row.projectSyncId,
        name: row.projectSyncId,
        sortOrder: Number.MAX_SAFE_INTEGER,
      });
    }
  }

  const byParent = new Map<string, ProjectNode[]>();
  for (const p of byId.values()) {
    // 父项目不在名单里就当根挂：树是从根往下走的，认不出的父会让整棵子树连同
    // 它的会话一起走不到。
    const parent =
      p.parentSyncId && byId.has(p.parentSyncId) ? p.parentSyncId : "";
    byParent.set(parent, [...(byParent.get(parent) ?? []), p]);
  }
  for (const siblings of byParent.values()) {
    siblings.sort(
      (a, b) =>
        a.sortOrder - b.sortOrder ||
        a.name.localeCompare(b.name) ||
        a.syncId.localeCompare(b.syncId),
    );
  }

  const rowsByProject = new Map<string, IndexGroupRow[]>();
  for (const row of rows) {
    if (!row.projectSyncId) continue;
    rowsByProject.set(row.projectSyncId, [
      ...(rowsByProject.get(row.projectSyncId) ?? []),
      row,
    ]);
  }

  const out: IndexGroup[] = [];
  const walk = (node: ProjectNode, depth: number): boolean => {
    const children = byParent.get(node.syncId) ?? [];
    const own = rowsByProject.get(node.syncId) ?? [];
    // 先占位再递归：父组必须排在子组之前，而「要不要留下」得等子树走完才知道。
    const at = out.length;
    out.push(
      emptyGroup({
        key: node.syncId,
        kind: "project",
        label: node.name,
        color: node.color,
        depth,
        rows: own,
      }),
    );
    let kept = false;
    for (const child of children) kept = walk(child, depth + 1) || kept;
    if (own.length === 0 && !kept) {
      out.splice(at, 1);
      return false;
    }
    return true;
  };
  for (const root of byParent.get("") ?? []) walk(root, 0);
  return out;
}

/**
 * Agent 轴：只摆有会话的 Agent（决策 10）。账号 Agent 名单在这里只是一张「标识 →
 * 名字 / 头像色」的查询表，不是组的来源。名单外但被会话引用到的标识如实自成一组，
 * 名字就用标识本身，不猜。
 */
function agentGroups(rows: IndexGroupRow[], agents: AgentInfo[]): IndexGroup[] {
  const known = new Map(agents.map((a) => [a.syncId, a]));
  const rowsByAgent = new Map<string, IndexGroupRow[]>();
  for (const row of rows) {
    if (!row.agentSyncId) continue;
    rowsByAgent.set(row.agentSyncId, [
      ...(rowsByAgent.get(row.agentSyncId) ?? []),
      row,
    ]);
  }
  return [...rowsByAgent.keys()]
    .map((syncId) => known.get(syncId) ?? { syncId, name: syncId })
    .sort(
      (a, b) =>
        a.name.localeCompare(b.name) || a.syncId.localeCompare(b.syncId),
    )
    .map((a) =>
      emptyGroup({
        key: a.syncId,
        kind: "agent",
        label: a.name,
        color: a.color,
        rows: rowsByAgent.get(a.syncId) ?? [],
      }),
    );
}

/**
 * 机器轴：一台机器一组。在线的排前面，各自按名字——离线机器沉底，而不是按名字
 * 混在中间让人一台台去看哪台还连得上。有会话的机器才出现。
 *
 * 认不出机器的行（发起端指纹不在设备名单里）自成最后一组：本体在 server 上，
 * 读它跟机器在不在没关系，因此这些行不能从索引里消失。
 */
function machineGroups(
  rows: IndexGroupRow[],
  machines: MachineInfo[],
  unknownLabel: string,
): IndexGroup[] {
  const byDevice = new Map(machines.map((m) => [m.deviceId, m]));
  const rowsByDevice = new Map<number, IndexGroupRow[]>();
  const unknown: IndexGroupRow[] = [];
  for (const row of rows) {
    if (row.deviceId === undefined) {
      unknown.push(row);
      continue;
    }
    rowsByDevice.set(row.deviceId, [
      ...(rowsByDevice.get(row.deviceId) ?? []),
      row,
    ]);
  }
  const groups = [...rowsByDevice.keys()]
    .map((deviceId) => ({
      deviceId,
      machine: byDevice.get(deviceId),
    }))
    .sort((a, b) => {
      const aOnline = a.machine?.online ? 0 : 1;
      const bOnline = b.machine?.online ? 0 : 1;
      return (
        aOnline - bOnline ||
        (a.machine?.name ?? "").localeCompare(b.machine?.name ?? "") ||
        a.deviceId - b.deviceId
      );
    })
    .map(({ deviceId, machine }) =>
      emptyGroup({
        key: `device-${deviceId}`,
        kind: "machine",
        label: machine?.name ?? "",
        depth: 0,
        offline: !machine?.online,
        rows: rowsByDevice.get(deviceId) ?? [],
      }),
    );
  if (unknown.length > 0) {
    // 认不出机器就不知道它在不在线：offline 保持 false，不替它编一个状态。
    groups.push(
      emptyGroup({
        key: UNKNOWN_MACHINE_KEY,
        kind: "machine",
        label: unknownLabel,
        rows: unknown,
      }),
    );
  }
  return groups;
}

/** 按最后活动时间倒序；没有时间的老会话（0）沉底而不是冒到最前面。 */
function byRecent(a: IndexGroupRow, b: IndexGroupRow): number {
  return b.updatedAt - a.updatedAt || a.key.localeCompare(b.key);
}

export function buildAxisGroups(
  axis: IndexAxis,
  input: AxisInput,
): IndexGroup[] {
  const byAgent = new Map(input.agents.map((a) => [a.syncId, a]));
  const byProject = new Map(input.projects.map((p) => [p.syncId, p]));
  const byMachine = new Map(input.machines.map((m) => [m.deviceId, m]));
  const rows = input.rows.map((r) => enrich(r, byAgent, byProject, byMachine));

  if (axis === "time") {
    // 一条会话都没有时连这一组都不摆：组里空无一物时它只是一个空壳，
    // 由宿主的空态承接（决策 10 在没有组头的这一轴上的同一条规则）。
    return rows.length === 0
      ? []
      : withTotals(
          [
            emptyGroup({
              key: "__all__",
              kind: "all",
              rows: [...rows].sort(byRecent),
            }),
          ],
          input.totals,
        );
  }

  let groups: IndexGroup[];
  let orphans: IndexGroupRow[];
  if (axis === "project") {
    groups = projectGroups(rows, input.projects);
    orphans = rows.filter((r) => !r.projectSyncId);
    if (orphans.length > 0) {
      groups.push(
        emptyGroup({
          key: UNASSIGNED_PROJECT_KEY,
          kind: "unassignedProject",
          label: input.labels?.unassignedProject ?? UNASSIGNED_PROJECT_KEY,
          rows: orphans,
        }),
      );
    }
  } else if (axis === "agent") {
    groups = agentGroups(rows, input.agents);
    orphans = rows.filter((r) => !r.agentSyncId);
    if (orphans.length > 0) {
      groups.push(
        emptyGroup({
          key: UNNAMED_AGENT_KEY,
          kind: "unnamedAgent",
          label: input.labels?.unnamedAgent ?? UNNAMED_AGENT_KEY,
          rows: orphans,
        }),
      );
    }
  } else {
    groups = machineGroups(
      rows,
      input.machines,
      input.labels?.unknownMachine ?? UNKNOWN_MACHINE_KEY,
    );
  }

  for (const g of groups) g.rows.sort(byRecent);
  return withTotals(groups, input.totals);
}

/**
 * 把宿主给的每组总数贴到组上。**没给的组不长这个字段**（不是 `total: undefined`）：
 * 「有没有分页」是这个字段在不在，写成一个恒存在、值可能为 undefined 的字段，
 * 消费方就分辨不出「宿主没分页」和「宿主分页了但这一组数不出来」。
 */
function withTotals(
  groups: IndexGroup[],
  totals: Record<string, number> | undefined,
): IndexGroup[] {
  if (!totals) return groups;
  for (const group of groups) {
    const total = totals[group.key];
    if (total !== undefined) group.total = total;
  }
  return groups;
}
