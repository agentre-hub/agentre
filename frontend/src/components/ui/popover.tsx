/**
 * Popover 已随会话索引（`SessionGroup` 的「查看全部 N」溢出入口）搬进
 * `@agentre-ai/agentre-ui` —— agentre-server 那侧既没有这个原语，也没法从
 * 宿主 `@/components/ui/*` 拿到它。
 *
 * 这里保留转发：仓库内的引用点全在，把它们逐个改指包只会淹没真正的改动
 * （与 `components/agentre/types.ts` 转发 `statusConfig` 同一处理）。
 */
export {
  Popover,
  PopoverAnchor,
  PopoverContent,
  PopoverTrigger,
} from "@agentre-ai/agentre-ui";
