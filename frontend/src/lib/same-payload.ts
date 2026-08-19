// frontend/src/lib/same-payload.ts
//
// samePayload: 「这次拉回来的快照和缓存里那份一模一样吗」。
//
// 侧栏的三个数据源(项目树 / chat-agents / 会话索引)在每轮对话起手与落定各校准一次,
// 而绝大多数轮次它们一个字节都没变。不比一比就换新数组的话,整个左栏(以及面包屑 /
// tab 视图)每轮白重渲两趟 —— 与 session-status-store 的同值短路是同一口径,只是那边
// 逐字段比、这边比的是整份列表快照。
//
// 用 JSON 序列化而不是深比较: 这些快照都是同一个 Wails 序列化器产出的纯数据,键序稳定;
// 真遇到序列化不了的值(循环引用等)就退化成「不相等」,只多一次重渲,不会出错。
export function samePayload(a: unknown, b: unknown): boolean {
  if (a === b) return true;
  try {
    return JSON.stringify(a) === JSON.stringify(b);
  } catch {
    return false;
  }
}
