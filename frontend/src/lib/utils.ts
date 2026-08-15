// cn() 的实现已经搬进共享包 @agentre-ai/agentre-ui（包内 src/lib/utils.ts）。
//
// 这一层转发是**刻意保留**的:全仓上百个文件都从 "@/lib/utils" 拿 cn,把它们
// 一次性改写成包路径会把搬迁的真实 diff 埋掉。新代码请直接从
// "@agentre-ai/agentre-ui" 导入 cn,这里只服务既有调用点。
export { cn } from "@agentre-ai/agentre-ui";
