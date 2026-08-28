// 紧凑相对时间的实现已经搬进共享包 @agentre-hub/agentre-ui（包内
// src/lib/relative-time.ts，与 i18n 形态、Intl 形态同一套档位阶梯）。
//
// 这一层转发是**刻意保留**的：8 个调用点从 "@/lib/relative-time" 取 relativeTime，
// 一次性改写它们会把搬迁的真实 diff 埋掉。新代码请直接从
// "@agentre-hub/agentre-ui" 导入 formatCompactRelativeTime。
export { formatCompactRelativeTime as relativeTime } from "@agentre-hub/agentre-ui";
