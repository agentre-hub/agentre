// Permission Mode = 后端 runtime 暴露的工具许可门级别(claudecode 4 档 / codex
// 2 档 / builtin 0 档)。集合从 runtime 的能力矩阵动态读;本文件只留 UI 元数据 +
// 通用 helper(归一化 / 循环切换 / claudecode bypass-lockout 判定)。
//
// 加新 backend 时:后端注册到自己的 PermissionModeMeta,两端前端都无需改动;若新
// 出现的 mode 字面量不在 PERMISSION_MODE_META_UI,会走兜底渲染(label = key,
// 灰色 pill,无图标)。
//
// **本文件不含任何 i18n 调用。** 元数据只带文案的 key,由渲染方在渲染期取词 ——
// 此前这张表在模块顶层调 `i18n.t(...)`,语言切换后不会重算,档位说明会一直停在
// 首次加载时的那门语言上。

// PermissionMode 是动态字符串(由 runtime 报的 allowedModes 决定);
// 保留 type alias 仅为可读性。
export type PermissionMode = string;

export interface PermissionModeMetaUI {
  key: PermissionMode;
  label: string;
  iconName:
    | "circle-question"
    | "shield-check"
    | "clipboard-list"
    | "shield-off";
  /** 档位说明的 i18n key;空串 = 这一档没有说明(兜底渲染)。 */
  descKey: string;
  /** Tailwind tone classes — 控件颜色按等级递增警示。 */
  pillClass: string;
  iconClass: string;
}

// PERMISSION_MODE_META_UI 是已知 mode 的展示元数据查找表。新 mode 没在表里时,
// 调用方走兜底渲染(fallbackPermissionModeMetaUI 或自行 fallback)。
//
// label 刻意**不走 t()**:它们是 CLI 自己的档位名(Claude 的 --permission-mode
// 取值),翻译它等于给同一个东西造第二个名字,用户在 CLI 与界面之间就对不上了。
export const PERMISSION_MODE_META_UI: Record<string, PermissionModeMetaUI> = {
  default: {
    key: "default",
    label: "Default",
    iconName: "circle-question",
    descKey: "permissionMode.meta.default.desc",
    pillClass: "bg-muted text-foreground border-border",
    iconClass: "text-muted-foreground",
  },
  acceptEdits: {
    key: "acceptEdits",
    label: "Accept Edits",
    iconName: "shield-check",
    descKey: "permissionMode.meta.acceptEdits.desc",
    pillClass: "bg-primary-soft text-primary-text border-primary-text/60",
    iconClass: "text-primary-text",
  },
  plan: {
    key: "plan",
    label: "Plan",
    iconName: "clipboard-list",
    descKey: "permissionMode.meta.plan.desc",
    pillClass:
      "bg-status-waiting-bg text-status-waiting border-status-waiting/60",
    iconClass: "text-status-waiting",
  },
  bypassPermissions: {
    key: "bypassPermissions",
    label: "Bypass",
    iconName: "shield-off",
    descKey: "permissionMode.meta.bypassPermissions.desc",
    pillClass: "bg-destructive-soft text-destructive border-destructive/60",
    iconClass: "text-destructive",
  },
};

// 未知 mode 的兜底元数据(label = key,通用 pill 样式,没有说明)。
export function fallbackPermissionModeMetaUI(
  key: PermissionMode,
): PermissionModeMetaUI {
  return {
    key,
    label: key || "Unknown",
    iconName: "circle-question",
    descKey: "",
    pillClass: "bg-muted text-foreground border-border",
    iconClass: "text-muted-foreground",
  };
}

// nextPermissionMode 按给定 order 循环切换。order 来自 runtime 报的
// permissionModeMeta.order(它决定 Shift+Tab 顺序)。order 为空时返回 current。
export function nextPermissionMode(
  current: PermissionMode,
  order: readonly string[],
): PermissionMode {
  if (order.length === 0) return current;
  const idx = order.indexOf(current);
  if (idx < 0) return order[0];
  return order[(idx + 1) % order.length];
}

// normalizePermissionMode 把任意 raw 值归一到 allowedModes 集合内:
//   - raw 在 allowedModes 中 → 返回 raw
//   - raw 缺失/非法,但 backendDefault 在 allowedModes 中 → 返回 backendDefault
//   - 否则返回 defaultMode(runtime 报的默认)
//
// 注:不做"启动后能否切到 bypass"的二次禁用(那是 isPermissionModeDisabled+ctx
// 的事),这里只校验 raw 在集合中。
export function normalizePermissionMode(
  raw: PermissionMode | null | undefined,
  allowedModes: readonly string[],
  defaultMode: PermissionMode,
  backendDefault?: PermissionMode | null,
): PermissionMode {
  if (typeof raw === "string" && raw.length > 0 && allowedModes.includes(raw)) {
    return raw;
  }
  if (
    typeof backendDefault === "string" &&
    backendDefault.length > 0 &&
    allowedModes.includes(backendDefault)
  ) {
    return backendDefault;
  }
  return defaultMode;
}

export interface PermissionModeDisableCtx {
  hasActiveSession?: boolean;
  permissionModeAtLaunch?: PermissionMode | null;
}

// isPermissionModeDisabled 判定某个 mode 在当前会话状态下是否被禁用。
//
// 唯一规则:**claudecode** 后端、会话已经启动(hasActiveSession=true)、且当前
// 会话不是以 bypass 启动的(permissionModeAtLaunch !== 'bypassPermissions')时,
// bypass 档置灰 —— Claude CLI 拒绝运行时进 bypass,必须新建会话首发选 bypass
// 才能解锁双向切换。
//
// 这条规则与具体 runtime CLI 行为绑定,因此保留 runtimeKey 参数(string,而非
// 枚举,避免硬绑死 backend 类型)。其它 runtime(codex/builtin/remote)不会触发
// 这条规则,统一返 false。
//
// 返回的是**布尔而不是文案**:调用方里有一半(循环切换时跳过禁用档)只关心真假,
// 让它为此拿到一句翻译好的话意味着这个纯函数必须持有 i18n 实例。原因文案由渲染
// 方在渲染期用 PERMISSION_MODE_DISABLED_REASON_KEY 取。
export function isPermissionModeDisabled(
  mode: PermissionMode,
  runtimeKey: string | null | undefined,
  ctx?: PermissionModeDisableCtx,
): boolean {
  if (runtimeKey !== "claudecode" || mode !== "bypassPermissions") {
    return false;
  }
  if (ctx?.hasActiveSession !== true) {
    return false;
  }
  return ctx?.permissionModeAtLaunch !== "bypassPermissions";
}

/** 被 isPermissionModeDisabled 判为禁用时，说明原因的 i18n key。 */
export const PERMISSION_MODE_DISABLED_REASON_KEY =
  "permissionMode.disabled.bypassAfterLaunch";
