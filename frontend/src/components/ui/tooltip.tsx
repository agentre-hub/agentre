// Tooltip 的实现已经搬进共享包 @agentre-ai/agentre-ui(包内 src/ui/tooltip.tsx)。
//
// 这一层转发是**刻意保留**的:8 个宿主文件从 "@/components/ui/tooltip" 拿这组符号,
// 把它们一次性改写成包路径会把搬迁的真实 diff 埋掉。新代码请直接从
// "@agentre-ai/agentre-ui" 导入,这里只服务既有调用点。
export {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@agentre-ai/agentre-ui";
