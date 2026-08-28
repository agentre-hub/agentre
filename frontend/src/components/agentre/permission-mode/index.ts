// PermissionModePill 与它的展示元数据 / 循环 / bypass 锁死判定住在
// `@agentre-hub/agentre-ui`：那一颗 pill 与 agentre-server 的控制台是同一颗，
// 两端各写一份就是同一件事被改两次或只改一次。**从包里取，不要在这里再包一层。**
//
// 留在宿主的只有下面这个 hook：它 import 了 Wails 绑定 SetChatPermissionMode，
// 而包不得 import 宿主耦合（packages/agentre-ui/src/boundary.test.ts 已钉）。
export { usePermissionMode } from "./use-permission-mode";
export type {
  UsePermissionModeOptions,
  UsePermissionModeReturn,
} from "./use-permission-mode";
