/**
 * 记忆的宿主中立视图契约（spec 2026-11-11「记忆页」「界面通则」）。
 *
 * 桌面端从 Wails 的 `memory_svc.MemoryItem` 投影过来，控制台将来从 HTTP 投影；
 * 包只认这一份形状。归属名、会话标题这些要查宿主数据的东西，由宿主算好放进来。
 */

/** 记忆所属的层。与 `memory_entity.OwnerKind` 同值。 */
export type MemoryOwnerKind = "global" | "department" | "agent" | "project";

/** 一个归属：全局的 id 恒为 0。 */
export type MemoryOwnerRef = { kind: MemoryOwnerKind; id: number };

/** 谁写下的这一条：用户本人，或某个会话里的 agent（可点回那个会话）。 */
export type MemoryCreatorView =
  | { kind: "user" }
  | {
      kind: "ai";
      /** 会话标题；宿主拿不到标题时给 agent 名。 */
      label: string;
      /** 打开那个会话；宿主不支持跳转时不给，只显示文字。 */
      onOpen?: () => void;
    };

export type MemoryEntryView = {
  id: number;
  summary: string;
  detail: string;
  pinned: boolean;
  archived: boolean;
  /** 有上一版可恢复。 */
  hasPrevious: boolean;
  creator: MemoryCreatorView;
  /** 毫秒 epoch。 */
  createdAt: number;
  /** 毫秒 epoch：最近修改。 */
  updatedAt: number;
  /** 最近一次变更做了什么、是谁做的；宿主拿不到时不给。 */
  lastChange?: MemoryChangeView;
};

/** 变更动作，与 `memory_entity.ChangeAction` 同值。置顶不算变更。 */
export type MemoryChangeAction =
  | "created"
  | "edited"
  | "archived"
  | "unarchived"
  | "restored"
  | "moved";

export type MemoryChangeView = {
  action: MemoryChangeAction | (string & {});
  actor: "user" | "ai";
};

/** 编辑表单交回的内容。 */
export type MemoryDraft = { summary: string; detail: string };

/**
 * 一次写入的结果。失败时 `reason` 是宿主翻好的一句业务事实（后端按语言渲染的错误
 * 文案）；拿不到可读原因（机器原话）就不给，包退回自己的通用句。
 */
export type MemoryWriteResult = { ok: true } | { ok: false; reason?: string };

/** 行菜单里的一项操作。 */
export type MemoryRowAction =
  | { type: "pin"; pinned: boolean }
  | { type: "archive" }
  | { type: "unarchive" }
  | { type: "restorePrevious" }
  | { type: "move"; targetKey: string }
  | { type: "delete" };

/**
 * 「移到…」里的一个目标层。`group` 是它所在那一组的标题（部门 / Agent / 项目），
 * 相邻的同组目标共用一个组标题；`depth` 用于按树缩进。
 */
export type MemoryMoveTarget = {
  key: string;
  label: string;
  group?: string;
  depth?: number;
};

export type MemoryRowProps = {
  entry: MemoryEntryView;
  /** 就地编辑中：由宿主持有，后台刷新不会把它关掉。 */
  editing: boolean;
  onEditingChange: (editing: boolean) => void;
  onSave: (draft: MemoryDraft) => Promise<MemoryWriteResult>;
  onAction: (action: MemoryRowAction) => Promise<MemoryWriteResult>;
  moveTargets: MemoryMoveTarget[];
  /** 「超出预算，本次不带」组里的行变淡。 */
  dimmed?: boolean;
};

/** agent 归属的「继承的」一组：一行一个来源层。 */
export type MemoryInheritedLayer = {
  key: string;
  label: string;
  count: number;
  onOpen: () => void;
};

export type MemoryEntryGroupsProps = {
  /** 正常条目那一组的标题（agent 归属是「自己的」）。 */
  normalLabel: string;
  /** 本次带上的正常条目，置顶在前（顺序由宿主 / 后端给定）。 */
  normal: MemoryEntryView[];
  overBudget: MemoryEntryView[];
  archived: MemoryEntryView[];
  inherited?: MemoryInheritedLayer[];
  editingId: number | null;
  onEditingChange: (id: number | null) => void;
  onSave: (id: number, draft: MemoryDraft) => Promise<MemoryWriteResult>;
  onAction: (id: number, action: MemoryRowAction) => Promise<MemoryWriteResult>;
  moveTargetsFor: (entry: MemoryEntryView) => MemoryMoveTarget[];
};
