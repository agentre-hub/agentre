// DropdownMenu 的实现已经搬进共享包 @agentre-ai/agentre-ui
// (包内 src/ui/dropdown-menu.tsx)。
//
// 合并时本仓这份是结构上的超集(子菜单 / 勾选项 / 单选项、碰撞避让、最大高度 +
// 内部滚动),整体被采纳;只有阴影换成了 token 化的 shadow-overlay —— 原来的
// shadow-md 是 Tailwind 内建值,绕开了 token 层。
//
// 这一层转发是**刻意保留**的:14 个宿主文件从 "@/components/ui/dropdown-menu" 拿这
// 组符号,把它们一次性改写成包路径会把搬迁的真实 diff 埋掉。新代码请直接从
// "@agentre-ai/agentre-ui" 导入,这里只服务既有调用点。
export {
  DropdownMenu,
  DropdownMenuPortal,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuLabel,
  DropdownMenuItem,
  DropdownMenuCheckboxItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuSeparator,
  DropdownMenuShortcut,
  DropdownMenuSub,
  DropdownMenuSubTrigger,
  DropdownMenuSubContent,
} from "@agentre-ai/agentre-ui";
