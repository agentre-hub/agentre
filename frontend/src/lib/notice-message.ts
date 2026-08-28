/**
 * 「只承载供应商切换 notice 的旁白行」这个判定已经归共享包所有 ——
 * agentre-server 的转录也要按同一条规则找生成指示器的宿主,两份实现必然漂移。
 * 判据本身与它为什么长这样,见包里 `generating-indicator.ts` 的注释。
 *
 * 这里留一个再导出,而不是让调用点各自改 import：那几处（chat-panel 的审批
 * overlay、use-chat-session 的续读）不在本次改动范围里。
 */
export { isNoticeOnlyMessage } from "@agentre-hub/agentre-ui";
