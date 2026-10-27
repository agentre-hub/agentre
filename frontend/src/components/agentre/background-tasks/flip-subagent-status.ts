import type { chat_svc } from "../../../../wailsjs/go/models";
import {
  applySubagentMeta,
  isAgrctlSubagentMeta,
} from "../../../stores/chat-block-reducers";

type ChatBlockSubagentData = Omit<chat_svc.ChatBlockSubagent, "convertValues">;

// flipSubagentStatusInMessages 在已持久化的 messages 里就地翻转 tool_use block 的
// subagent.status(+ summary)。后台任务的完成是跨轮的(autonomous 轮携带
// completedTask):完成事件到达时,发起它的主轮早已结束,那条 tool_use block 已从
// liveBlocks 落进 messages —— 所以只改 liveBlocks(mergeSubagentMeta)翻不到它,面板
// 与行内 pill 会永远 spin。这里按 toolUseId 命中 messages 里的块并翻状态,免整会话
// 重载即可停转。
//
// 不可变更新:命中才返回新数组/新消息/新块引用,未命中(或空参)原样返回同一引用,
// 让 React setState 跳过无谓 re-render。
export function flipSubagentStatusInMessages(
  messages: chat_svc.ChatMessage[],
  toolUseId: string,
  status: string,
  summary?: string,
): chat_svc.ChatMessage[] {
  if (!status) return messages;
  return mergeSubagentMetaInMessages(messages, toolUseId, {
    status,
    ...(summary ? { summary } : {}),
  } as chat_svc.ChatBlockSubagent);
}

// mergeSubagentMetaInMessages 把一份 subagent meta 合并进 messages 里命中 toolUseId 的
// tool_use block。后台 subagent 在会话空闲态跑的那段时间,CLI 一直吐 task_progress,而它
// 的派遣卡早已从 liveBlocks 落进 messages —— store 的 mergeSubagentMeta 只翻 liveBlocks,
// 合并必然落空,卡片上的工具数 / token 会一直停在派遣那一刻。
//
// 合并语义与 store 侧一致:runs 是完整快照，出现时整段替换；undefined/省略时保留旧值。
//
// 不可变更新:命中才返回新数组/新消息/新块引用,未命中(或空参)原样返回同一引用,
// 让 React setState 跳过无谓 re-render。
export function mergeSubagentMetaInMessages(
  messages: chat_svc.ChatMessage[],
  toolUseId: string,
  meta: ChatBlockSubagentData,
): chat_svc.ChatMessage[] {
  if (!toolUseId || !meta) return messages;

  // agrctl 升级事件可能落在还没有 overlay 的 shell 块上(后台调用早已落库、此前从没有
  // 过 task 帧):此时按 toolUseId 命中即原地升级;其它事件仍要求块已有 overlay。
  const upgrade = isAgrctlSubagentMeta(meta);
  let hit = false;
  const next = messages.map((m) => {
    let blockHit = false;
    const blocks = (m.blocks ?? []).map((b) => {
      if (
        b.type !== "tool_use" ||
        b.toolUseId !== toolUseId ||
        (!b.subagent && !upgrade)
      ) {
        return b;
      }
      blockHit = true;
      return {
        ...b,
        subagent: applySubagentMeta(b.subagent, meta),
      } as chat_svc.ChatBlock;
    });
    if (!blockHit) return m;
    hit = true;
    return { ...m, blocks } as chat_svc.ChatMessage;
  });

  return hit ? next : messages;
}
