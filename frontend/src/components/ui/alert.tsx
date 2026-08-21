// Alert 的实现已经搬进共享包 @agentre-ai/agentre-ui(包内 src/ui/alert.tsx)。
//
// 合并后多了一档 `warning`(取色 status-waiting):原本只有 agentre-server 那份有它,
// 用在「需要你处理,但还不是故障」的状态上。本仓暂时没有调用点,但让它继续在另一个
// 宿主里以副本形式存在的代价更大 —— 副本正是靠「就差这一点点」重新长出来的。
//
// 这一层转发是**刻意保留**的:10 个宿主文件从 "@/components/ui/alert" 拿这组符号,
// 把它们一次性改写成包路径会把搬迁的真实 diff 埋掉。新代码请直接从
// "@agentre-ai/agentre-ui" 导入,这里只服务既有调用点。
export { Alert, AlertTitle, AlertDescription } from "@agentre-ai/agentre-ui";
