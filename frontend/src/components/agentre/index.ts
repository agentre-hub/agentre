export { AgentAvatar, SidebarButton, StatusPill } from "./primitives";
export { AppStatusBar, AppTopBar, ThemeToggle } from "./chrome";
export { AgentGroup, SessionRow } from "./agent-list";
export { SessionIndexPage } from "./session-index/index-page";
export { ChatStreamsHost } from "./chat-streams-host";
export { TurnCompleteNotifier } from "./turn-complete-notifier";
export { SyncAppliedHost } from "./sync-applied-host";
export { NotificationToastViewport } from "./notification-toast";
export { QuitConfirmDialog } from "./quit-confirm-dialog";
export { ExternalApprovalDialog } from "./external-approval-dialog";
export { ApprovalGate, ChatComposer } from "./chat";
export {
  ShortcutsProvider,
  ChatTabsShortcuts,
  isPrimaryShortcut,
} from "./shortcuts";
// CommandPalette 按需加载（App.tsx 里 lazy import 其模块路径），这里只导出常驻的作用域桥。
export { PaletteScopeBridge } from "./command-palette/palette-scope-bridge";
export type { DesktopPlatform } from "./chrome";
