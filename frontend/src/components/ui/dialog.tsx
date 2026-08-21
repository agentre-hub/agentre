// Dialog 的实现已经搬进共享包 @agentre-ai/agentre-ui(包内 src/ui/dialog.tsx)。
//
// 搬之前它有三份:本仓一份、agentre-server 一份、包内 engine/ui/ 还私藏一份给引擎
// 面板用。三份逐行对应却各自漂了几处 —— 本仓与包内那份的遮罩是 bg-slate-900/25
// 这样的调色板字面色,只有 agentre-server 那份是 bg-scrim;而窄视口下的
// w-[calc(100%-2rem)] 又只有它有。合并后逐处取「更 token 化的那一份」,所以本仓
// 的遮罩颜色与标题字号会随之变化,那是有意的收敛。
//
// 这一层转发是**刻意保留**的:21 个宿主文件从 "@/components/ui/dialog" 拿这组符号,
// 把它们一次性改写成包路径会把搬迁的真实 diff 埋掉。新代码请直接从
// "@agentre-ai/agentre-ui" 导入,这里只服务既有调用点。
export {
  Dialog,
  DialogTrigger,
  DialogPortal,
  DialogOverlay,
  DialogClose,
  DialogContent,
  DialogHeader,
  DialogBody,
  DialogFooter,
  DialogTitle,
  DialogDescription,
} from "@agentre-ai/agentre-ui";
