/**
 * 项目这一面的宿主边界（规格 2026-08-22，决策 7）。
 *
 * 两端的数据形状根本不同：桌面端是数字 `id` 经 wailsjs，`agentre-server` 是字符串
 * `syncId` 经 REST。所以包里不认识任何一端的后端，只认这里这几个 view 与 port —— 与
 * `engine/ports.ts` 同一条路子，那一组已经带着引擎面板跨两端服役。
 *
 * **能力差异用可选 port 表达，不用 `isDesktop` 分支**：没有那个 port 就没有那个入口。
 */

import type * as React from "react";

/** 选择器左栏里的一台机器。`id` 在服务端是指纹、在桌面端是 deviceID，包不关心。 */
export interface PickerMachine {
  id: string;
  name: string;
  /** 只影响图标与文案，不影响能不能选。 */
  kind: "agentred" | "desktop";
  online: boolean;
}

/** 目录里的一条。`size` / `mtime` 本轮没有呈现件用得上，故不进这个契约。 */
export interface DirectoryEntry {
  name: string;
  isDir: boolean;
  /** 符号链接：挂一枚角标，不改变能不能选。 */
  symlink?: boolean;
}

export interface ListDirResult {
  /** 那台机器解析后的**绝对路径**：请求里传空串就是它的 $HOME，以返回的为准。 */
  path: string;
  entries: DirectoryEntry[];
  /** 条目过多时为 true —— 只列出了前若干项，**不能假装这就是全部**。 */
  truncated: boolean;
  /**
   * **当前这个目录**是不是 git 仓库。
   *
   * 是当前目录、不是每个子目录：判据是这次列出来的条目里有没有 `.git`，而子目录里
   * 有什么，这一次请求根本没读。曾经把它画成逐行角标——那是画了一个后端答不出来的东西。
   */
  isGitRepo?: boolean;
}

/**
 * 一次失败落在哪一类。
 *
 * 分得开是有代价才做的：「空目录」与「不让看」在界面上必须是两件不同的事——把权限
 * 拒绝画成一个空目录，用户会以为那台机器上什么都没有。
 */
export type DirectoryFailureKind =
  | "denied"
  | "notFound"
  | "notDir"
  | "refused"
  | "exists"
  | "invalidName"
  | "disconnected"
  | "unknown";

export interface DirectoryFailure {
  kind: DirectoryFailureKind;
  /** 宿主给的原文，只在 `unknown` 那一档兜底显示。 */
  message: string;
}

/**
 * port **交出判别式结果，而不是抛错**。
 *
 * 错误分类是 wire 相关的：`agentre-server` 读的是 JSON-RPC 错误码（-32030..-32035），
 * 桌面端读的是 wails 那侧的 Go 错误串。让宿主分好类再交进来，包就不必去猜一个它读不懂
 * 的 error 属于哪一档 —— 猜错的代价正是上面那句「把权限拒绝画成空目录」。
 */
export type FsOutcome<T> =
  | { ok: true; result: T }
  | { ok: false; failure: DirectoryFailure };

export type ListDirOutcome = FsOutcome<ListDirResult>;
export type MkdirOutcome = FsOutcome<void>;

export interface ProjectFsPort {
  /**
   * 读一个目录。`path` 传空串 = 那台机器的 $HOME。
   *
   * 连接还没就绪时**让 promise 挂着**，不要立刻回一个 `disconnected` —— 界面靠
   * 「请求在飞」画加载态，回一个失败就变成每次打开都先说一句「连接断了」。
   */
  listDir(machineId: string, path: string): Promise<ListDirOutcome>;
  mkdir(machineId: string, parent: string, name: string): Promise<MkdirOutcome>;
}

/** `parent` 下的 `name` 拼成绝对路径。根目录不重复斜杠。 */
export function joinPath(parent: string, name: string): string {
  if (!parent || parent === "/") return `/${name}`;
  return `${parent.replace(/\/+$/, "")}/${name}`;
}

/** 面包屑：从根一路到当前目录，每一节带它自己的绝对路径。 */
export function breadcrumbOf(path: string): { name: string; path: string }[] {
  const crumbs = [{ name: "/", path: "/" }];
  let walked = "";
  for (const part of path.split("/").filter(Boolean)) {
    walked = `${walked}/${part}`;
    crumbs.push({ name: part, path: walked });
  }
  return crumbs;
}

/**
 * 目录名当场就判得出来的那几种不合法：含斜杠、首尾有空白、`.` 或 `..`、过长。
 *
 * 判在这里而不是等那台机器回一个错：这几种在任何一端都建不出来，白跑一趟往返只是
 * 让用户多等一秒再看到同一句话。真正要机器才知道的（重名、权限）仍然由 port 回。
 */
const INVALID_FOLDER_NAME = /[/]|^\s|\s$|^\.\.?$/;
const MAX_FOLDER_NAME = 255;

export function isValidFolderName(name: string): boolean {
  return (
    name.length > 0 &&
    name.length <= MAX_FOLDER_NAME &&
    !INVALID_FOLDER_NAME.test(name)
  );
}

/**
 * 目录在前、同类按名字排，数字按人读的顺序（`item2` 在 `item10` 前面）。
 *
 * 后端返回什么顺序不该漏到界面上——桌面端本来就在排，合并时不能把它丢掉。
 */
export function sortEntries(entries: DirectoryEntry[]): DirectoryEntry[] {
  return [...entries].sort((a, b) => {
    if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
    return a.name.localeCompare(b.name, undefined, { numeric: true });
  });
}

// ─── 项目设置这一面 ───────────────────────────────────────────────────────────

/**
 * 一次写失败落在哪一类。
 *
 * 只有中继写（喊某台机器自己写它的路径）真正分得出类；其余的写（改字段、增删成员）
 * 由服务端给一句自带文案的业务码，宿主抽出来原样交进 `message`，分类交 `unknown`。
 */
export type ProjectWriteFailureKind =
  | "notSynced"
  | "pathNotFound"
  | "invalidPath"
  | "disconnected"
  | "unknown";

export interface ProjectWriteFailure {
  kind: ProjectWriteFailureKind;
  /**
   * 宿主已经抽好的业务文案。
   *
   * **只在 `unknown` 那一档显示**：分得出类时包有自己写好的那一句，而宿主手里那串
   * （Go 的 relay 错误文本、HTTP 状态行）对用户没用。分不出类时反过来 —— 服务端的
   * 业务码自带一句人话（「该 Agent 已经是这个项目的成员」），吞掉它折成「保存失败」
   * 等于让人对着一个按了没反应的按钮猜。
   */
  message?: string;
}

export type ProjectWriteOutcome =
  | { ok: true }
  | { ok: false; failure: ProjectWriteFailure };

/** 这个项目在某台机器上的那一行。 */
export interface ProjectMachineView {
  id: string;
  name: string;
  kind: "agentred" | "desktop";
  online: boolean;
  /** 空串 = 还没配。 */
  path: string;
  /**
   * 宿主自己就是这台机器（桌面端的本机）：带「本机」角标，且挑目录走原生对话框。
   * web 宿主没有这样一行。
   */
  isSelf?: boolean;
  /**
   * 改这一行的路径，要不要那台机器在线。
   *
   * **由「写往哪去」决定，不由机器类型决定** —— 经中继喊那台机器自己写才要它在线
   * （agentre-server 上别人的桌面端：路径只住在它的上报组，服务端写一行下次上报就
   * 被冲掉）；宿主自己写得了的都不要（本机、账号级同步对象、桌面端本地那张位置表）。
   *
   * 这是一件 wire 事实，只有宿主知道，所以由宿主说出来；包只负责「要在线而它不在线
   * 时这一行怎么表现」。**与「能不能浏览目录」是两件事**：浏览一律要那台机器在线
   * （离线的机器答不出目录里有什么），此前两者被同一个 disabled 绑在了一起，于是
   * 离线的 agentred 明明配得了路径却连输入框都是灰的。
   */
  writeNeedsOnline?: boolean;
  /**
   * 这一行有没有可移除的东西。两类机器问的不是同一件事：agentred 问「同步组里有没有
   * 那一行」，桌面端问「那台机器上配了没有」—— 由宿主判，包不认识任何一端的存储。
   */
  removable: boolean;
}

export interface ProjectMemberView {
  /** 宿主用来定位这条成员关系的键（本仓是 member syncId，桌面端是 agentID）。 */
  id: string;
  name: string;
  /** 颜色 token，画身份方块用。 */
  color?: string;
  avatarIcon?: React.ReactNode;
  avatarDataUrl?: string;
  /** 继承自祖先项目：能用，但**不在这个项目里**，所以这里移除不了它。 */
  inherited?: boolean;
  /** 继承来源的项目名。 */
  inheritedFrom?: string;
}

export interface ProjectCandidateView {
  id: string;
  name: string;
  color?: string;
  avatarIcon?: React.ReactNode;
  avatarDataUrl?: string;
  /**
   * 加不了。**留在列表里并说出原因**，不是静默消失 —— 一个用户以为自己有的 Agent
   * 凭空不见，比一行灰字更让人找不着北。
   */
  disabled?: boolean;
  disabledReason?: string;
}

export interface ProjectSettingsView {
  id: string;
  name: string;
  description: string;
  /** 图标 key。解成图标是宿主的事（见 `ProjectSettingsDialogProps.iconField`）。 */
  icon?: string;
  /** 颜色 token，如 "agent-3"。 */
  color?: string;
  /** 空串 = 挂在根上。 */
  parentId: string;
  members: ProjectMemberView[];
  /** 还不是成员、可以加进来的。 */
  candidates: ProjectCandidateView[];
}

/** 一次改字段要写哪几格。只递改动的那几格 —— 值没变的不发。 */
export interface ProjectFieldValues {
  name?: string;
  description?: string;
  icon?: string;
  color?: string;
  parentId?: string;
}

export interface ProjectSettingsPorts {
  updateFields(
    projectId: string,
    fields: ProjectFieldValues,
  ): Promise<ProjectWriteOutcome>;
  addMember(
    projectId: string,
    candidateId: string,
  ): Promise<ProjectWriteOutcome>;
  /**
   * 递整条成员 view 而不是一个 id：两端拿来定位的键不是同一个东西（本仓删的是成员
   * 关系那条记录，桌面端删的是「项目 × Agent」这一对），递整条让宿主自己挑。
   */
  removeMember(
    projectId: string,
    member: ProjectMemberView,
  ): Promise<ProjectWriteOutcome>;
  /**
   * 读这个项目的机器清单。
   *
   * 这一个**允许 reject**（与 `ProjectFsPort` 那几个不同）：那边要分类，这边不用——
   * 界面只问「读上来了没有」，没有第二种失败要分辨。包会 catch 并落到 failed 那一档。
   */
  listMachines(projectId: string): Promise<ProjectMachineView[]>;
  /**
   * 写某台机器上的路径。**写往哪去只由那台机器是哪一类决定**，由宿主判：
   * agentred 的路径是账号级同步对象（服务端直写，离线也配得了），桌面端的只住在它
   * 自己的上报组（经中继喊它自己写，因此它必须在线）。
   */
  setMachinePath(
    projectId: string,
    machine: ProjectMachineView,
    path: string,
  ): Promise<ProjectWriteOutcome>;
  clearMachinePath(
    projectId: string,
    machine: ProjectMachineView,
  ): Promise<ProjectWriteOutcome>;
  /** 挑目录用的传输，递给包里的目录选择器。 */
  fs: ProjectFsPort;
  /**
   * 宿主自己那台机器的**原生**目录对话框（桌面端挂，web 不挂）。
   *
   * 本机目录用系统原生对话框比任何自绘面板都好，没有理由为了截图上看起来一样而
   * 把它换掉（决策 11）。返回 null = 用户取消了。
   */
  pickLocalDirectory?(): Promise<string | null>;
}

// ─── 新建 / 删除 ─────────────────────────────────────────────────────────────

/** 一次新建要送下去的东西。**只出现真的填了的键**，没填的不翻成空串。 */
export interface ProjectCreateDraft {
  name: string;
  description?: string;
  icon?: string;
  color?: string;
  parentId?: string;
  /** 宿主挂了挑本机目录的能力才可能有它。 */
  localPath?: string;
}

export type ProjectCreateOutcome =
  | { ok: true; id: string }
  | { ok: false; failure: ProjectWriteFailure };

/** 本机 git 探测的结果。`isGitRepo` 为 false 时后两格无意义。 */
export interface ProjectGitInfo {
  isGitRepo: boolean;
  branch?: string;
  origin?: string;
}

export interface ProjectCreatePorts {
  create(draft: ProjectCreateDraft): Promise<ProjectCreateOutcome>;
  /**
   * 宿主自己那台机器的原生目录对话框。**挂了才有「本机路径」那一格**，
   * web 宿主不挂 —— 没有那个 port 就没有那个入口，不用 `isDesktop` 分支。
   */
  pickLocalDirectory?(): Promise<string | null>;
  /**
   * 探一探这个本机目录是不是 git 仓库。可选：只有摸得到本机文件系统的宿主挂得上。
   * 返回 null = 探不出来，此时什么都不标（编一个「不是仓库」比不说更糟）。
   */
  probeGitRepo?(path: string): Promise<ProjectGitInfo | null>;
  /**
   * 这一端的后端**建不出没有路径的项目**，所以本机路径是必填的。
   *
   * 规格决策 9 要的是「路径不必填」，两端同一套校验 —— 这个开关是那条决策今天在
   * 桌面端**还落不了地**的如实记录，不是一个长期的产品选项：桌面端的
   * `ProjectCreateRequest`（internal/app/project.go）没有 `LocalPathMissing` 这一格，
   * 而 `Project.Check` 在它为 false 时要求 `Path` 非空，于是空路径必被后端拒。
   * 让按钮亮着然后必然失败，比当场说「这一格得填」更糟。补齐要动 Go —— 本轮的硬
   * 不变量禁止，所以先把这件事说出口。agentre-server 不设它。
   */
  localPathRequired?: boolean;
}

export interface ProjectDeletePorts {
  deleteProject(projectId: string): Promise<ProjectWriteOutcome>;
}
