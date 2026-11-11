import type { ProjectWriteFailure } from "@agentre-hub/agentre-ui";

import { splitErrorDetail } from "./error-detail";

/**
 * 管理页的错误分类（规格 2026-11-05 S5）：页面按 kind 决定说哪句中文事实、给不给出口。
 *
 * 判据只有一条：`internal/app/coded_error.go` 写在过桥错误前面的
 * `agentre-code:<码> <后端本地化文案>`。不按文案反猜 —— 那一改文案就静默失灵。
 * 没有码（或码还没映射）一律 `unknown`：编一个类比说「不知道」更糟。
 */
export type AppErrorKind =
  | "offline"
  | "conflict"
  | "notFound"
  | "invalidFile"
  | "permission"
  | "unknown";

export type AppError = {
  kind: AppErrorKind;
  /**
   * 能直接当正文的那句话：后端按语言渲染好的业务文案（前缀已剥掉）。
   * 错误不带码时为空串 —— 那是机器原话，页面用自己的中文标题，原文只进 detail。
   */
  message: string;
  /** 原始细节，只进默认折叠的「详情」：不带码时是整段原文，带码时是文案后面的 cause。 */
  detail?: string;
};

const CODED_PREFIX = /^agentre-code:(\d+)\s?([\s\S]*)$/;

/**
 * 拆开过桥串前面的 `agentre-code:<码> ` —— 这条契约在前端的**唯一**解析点。
 * 不带前缀返回 null；带前缀时 message 是剥掉前缀后的余下全文（前缀是内部记号，
 * 不该出现在用户读到的那句话里）。
 */
export function parseCodedError(
  raw: string,
): { code: number; message: string } | null {
  const m = CODED_PREFIX.exec(raw);
  return m ? { code: Number(m[1]), message: m[2] } : null;
}

/**
 * 业务码 → 类别。数值出处是 `internal/pkg/code/code.go`（稳定 wire 值），注释是常量名。
 */
const KIND_BY_CODE: Record<number, AppErrorKind> = {
  // offline：那台机器 / 服务此刻够不着，等它回来再试
  17018: "offline", // ChatRemoteDaemonUnreachable
  20301: "offline", // ServerUnreachable
  20405: "offline", // RemoteDeviceDialFailed
  20409: "offline", // RemoteDeviceTimeout
  20500: "offline", // RemoteRunnerDialFailed
  20502: "offline", // RemoteRunnerConnectionLost
  20604: "offline", // RemoteFsDeviceOffline
  20804: "offline", // WorkspaceFsDeviceOffline
  20911: "offline", // ChatImportDeviceOffline
  21000: "offline", // PortForwardDeviceOffline
  // conflict：和现有数据撞上了，换个做法才行
  13001: "conflict", // DepartmentNameDuplicated
  13005: "conflict", // DepartmentCircularReference
  14001: "conflict", // AgentNameDuplicated
  14011: "conflict", // AgentCircularReference
  18001: "conflict", // ProjectNameDuplicated
  18007: "conflict", // ProjectCircularReference
  18010: "conflict", // ProjectHasChildren
  18011: "conflict", // ProjectHasActiveSessions
  18103: "conflict", // ProjectLocationDuplicate
  18206: "conflict", // IssueLabelNameDuplicated
  20605: "conflict", // RemoteFsMkdirExists
  20707: "conflict", // DataImportDuplicateLocal
  21002: "conflict", // PortForwardPortTaken
  // notFound：要动的东西已经不在了
  10002: "notFound", // NotFound
  13000: "notFound", // DepartmentNotFound
  13003: "notFound", // DepartmentParentNotFound
  14000: "notFound", // AgentNotFound
  14005: "notFound", // AgentDepartmentNotFound
  14010: "notFound", // AgentParentNotFound
  17000: "notFound", // ChatSessionNotFound
  18000: "notFound", // ProjectNotFound
  18004: "notFound", // ProjectPathNotExist
  18005: "notFound", // ProjectParentNotFound
  18100: "notFound", // ProjectLocationNotFound
  18200: "notFound", // IssueNotFound
  18205: "notFound", // IssueLabelNotFound
  20400: "notFound", // RemoteDeviceNotFound
  20602: "notFound", // RemoteFsNotFound
  20805: "notFound", // WorkspaceFsNotFound
  21001: "notFound", // PortForwardNotDeclared
  // invalidFile：选中的文件不是能导入的那种
  20700: "invalidFile", // DataBundleFormatInvalid
  20701: "invalidFile", // DataBundleVersionUnsupported
  20702: "invalidFile", // DataBundleScopeUnknown
  // permission：被拒绝了，要去那一侧放开
  20404: "permission", // RemoteDeviceUnauthorized
  20600: "permission", // RemoteFsPathRefused
  20601: "permission", // RemoteFsPermDenied
  20801: "permission", // WorkspaceFsPathRefused
};

/** 一次失败的原始文本：Wails 拒绝时可能是 Error、裸字符串或带 message 的对象。 */
export function errorText(e: unknown): string {
  if (e === undefined || e === null) return "";
  if (e instanceof Error) return e.message;
  if (typeof e === "string") return e;
  if (typeof e === "object" && "message" in e) {
    const { message } = e as { message: unknown };
    if (typeof message === "string") return message;
  }
  return String(e);
}

/** 把一次失败（Wails 拒绝时可能是 Error，也可能是裸字符串）分成一类。 */
export function classifyAppError(e: unknown): AppError {
  const raw = errorText(e).trim();
  const coded = parseCodedError(raw);
  if (!coded) {
    return { kind: "unknown", message: "", detail: raw || undefined };
  }
  const { msg, detail } = splitErrorDetail(coded.message);
  return {
    kind: KIND_BY_CODE[coded.code] ?? "unknown",
    message: msg.trim(),
    detail,
  };
}

/**
 * 项目写入（新建 / 改字段 / 移动 / 成员 / 本机路径 / 远端位置）的码 → 共享包那几类。
 * 只列这一端真能产出的码；`notSynced` 是中继写才有的一类，桌面端的写不经那台机器。
 */
const PROJECT_WRITE_KIND_BY_CODE: Record<number, ProjectWriteFailure["kind"]> =
  {
    18003: "invalidPath", // ProjectInvalidPath
    18004: "pathNotFound", // ProjectPathNotExist
    18101: "invalidPath", // ProjectLocationInvalidPath
  };

/**
 * 把一次项目写入失败翻成共享包认识的 `ProjectWriteFailure`。
 *
 * 分得出类时包说它自己那一句；分不出类时，带码的那句后端业务文案（「同级下已存在
 * 同名项目」）进 `message` 由包原样透出。不带码的是机器原话，不进 `message`，
 * 包退回它自己的通用句。
 */
export function classifyProjectWriteError(e: unknown): ProjectWriteFailure {
  const raw = errorText(e).trim();
  const coded = parseCodedError(raw);
  if (!coded) return { kind: "unknown", message: undefined };
  const message = splitErrorDetail(coded.message).msg.trim() || undefined;
  const kind =
    PROJECT_WRITE_KIND_BY_CODE[coded.code] ??
    (KIND_BY_CODE[coded.code] === "offline" ? "disconnected" : "unknown");
  return { kind, message };
}
