/**
 * 项目这一面的宿主边界（规格 2026-08-22，决策 7）。
 *
 * 两端的数据形状根本不同：桌面端是数字 `id` 经 wailsjs，`agentre-server` 是字符串
 * `syncId` 经 REST。所以包里不认识任何一端的后端，只认这里这几个 view 与 port —— 与
 * `engine/ports.ts` 同一条路子，那一组已经带着引擎面板跨两端服役。
 *
 * **能力差异用可选 port 表达，不用 `isDesktop` 分支**：没有那个 port 就没有那个入口。
 */

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
