// 行模型:从「这一轮拿到的消息」到「虚拟行」。
//
// 四段推导串在一起,顺序不能换:
//   1. messages —— 切掉正文还没取回的前缀(那种消息手上只有元数据,当整条渲染出来的是
//      一份缺了工具结果 / 思考 / 嵌套卡的假转录);
//   2. 压缩折叠 —— 找最后一条 compact_boundary,把它之前的消息藏起来(expanded 退化);
//   3. autonomousIds —— 自主续轮的消息 id 集合(判定归共享包,用**完整**消息表算);
//   4. rows —— persisted 行缓存 + live 内容叠加,外加两张索引图。
// 每段为什么这么写,都记在它自己那段注释里,搬过来时一字未改。
//
// 这里是转录里唯一「只读消息、产出行」的地方:虚拟器、跳转、行视图都只消费它的输出,
// 谁也不反过来改它的推导。
import * as React from "react";
import { useShallow } from "zustand/react/shallow";

import {
  applyLiveTranscriptRows,
  autonomousTurnMessageIds,
  buildSettledTranscriptRows,
  buildSourceByMessageId,
  indicatorHostMessageId,
  type TranscriptRow,
} from "@agentre-hub/agentre-ui";

import { useLocalCommandsStore } from "@/stores/local-commands-store";
import { RemoteDeviceFingerprint } from "../../../../../wailsjs/go/app/App";
import { chat_svc } from "../../../../../wailsjs/go/models";
// 只借组件的 live 内容契约(它比包的 LiveRowContent 多带 liveRetry / liveTurn);
// type-only 导入,不会和 ../transcript 成运行时环。
import type { TranscriptLiveContent } from "../transcript";

// findLastCompactBoundary 顺序扫所有 messages.blocks 找最后一条 type=compact_boundary
// 的位置;没找到返回 null。返回 (messageIdx, blockIdx) 让 ChatTranscript 知道从哪里
// 起算"压缩后"显示段:messages[messageIdx].blocks[blockIdx] 即 boundary 块本身。
function findLastCompactBoundary(
  messages: chat_svc.ChatMessage[],
): { messageIdx: number; blockIdx: number } | null {
  let found: { messageIdx: number; blockIdx: number } | null = null;
  messages.forEach((m, i) => {
    (m.blocks ?? []).forEach((b, j) => {
      if (b.type === "compact_boundary") {
        found = { messageIdx: i, blockIdx: j };
      }
    });
  });
  return found;
}

// NO_LOADED_MESSAGES 是「一条正文都还没取到」时的稳定空表 —— 每次渲染新建 [] 会让
// 下游按数组身份做的记忆化整片失效。
const NO_LOADED_MESSAGES: chat_svc.ChatMessage[] = [];

// useLocalDeviceFingerprint 交出本机设备指纹(R17 本机判定)。指纹是 keychain 里
// 的稳定值,模块级缓存一次 Wails 调用,进程内不再重复请求;组件用它计算
// sourceByMessageId(本机发出的用户消息不进来源表)。
let localFingerprintPromise: Promise<string> | null = null;

function getLocalFingerprintPromise(): Promise<string> {
  if (!localFingerprintPromise) {
    localFingerprintPromise = RemoteDeviceFingerprint().catch(() => "");
  }
  return localFingerprintPromise;
}

function useLocalDeviceFingerprint(): string | undefined {
  const [fp, setFp] = React.useState<string | undefined>(undefined);
  React.useEffect(() => {
    let alive = true;
    void getLocalFingerprintPromise().then((v) => {
      if (alive) setFp(v || undefined);
    });
    return () => {
      alive = false;
    };
  }, []);
  return fp;
}

export function useTranscriptRows({
  allMessages,
  liveByMessageId,
  sessionId,
}: {
  /** 会话的完整消息表(含正文尚未取回的那段前缀)。 */
  allMessages: chat_svc.ChatMessage[];
  /** 各消息此刻的流式内容,按 messageId 索引;表里有 key 的消息才在流式中。 */
  liveByMessageId?: ReadonlyMap<number, TranscriptLiveContent>;
  /** 取本会话的临时本地命令条目用。 */
  sessionId?: number;
}): {
  rows: TranscriptRow[];
  firstRowIndexByMessageId: ReadonlyMap<number, number>;
  rowIndexByKey: ReadonlyMap<string, number>;
  /** 切掉未取正文前缀、且折叠后的消息表。 */
  messages: chat_svc.ChatMessage[];
  /** 正文在取回中,或更早那段还没取。 */
  folding: boolean;
  /** 被折叠掉的消息条数。 */
  foldedCount: number;
  /** 展开被折叠的那一段。 */
  setExpanded: React.Dispatch<React.SetStateAction<boolean>>;
  /** 生成指示器(三个点)的宿主消息 id。 */
  lastAssistantId: number | null;
} {
  // 转录只渲染正文已经取全的消息。窗口外的消息手上只有元数据加派生视图点名的那几类
  // 块(见 use-chat-session 的 DERIVED_VIEW_BLOCK_TYPES),把它们当整条渲染,用户看到
  // 的是一份缺了工具结果 / 思考 / 嵌套卡的假转录 —— 宁可先不渲染,由顶部的入口取回来
  // 之后自然接上。未取正文的消息永远是列表**前缀**(窗口取的是末尾一段),所以这里
  // 切一刀就够;整表都已就绪时连数组引用一起原样传下去,下游的记忆化不被击穿。
  const messages = React.useMemo(() => {
    const first = allMessages.findIndex((m) => m.blocksLoaded !== false);
    if (first === 0) return allMessages;
    return first < 0 ? NO_LOADED_MESSAGES : allMessages.slice(first);
  }, [allMessages]);

  // lastAssistantId:生成指示器(三个点)的宿主。规则归共享包所有 ——
  // agentre-server 的转录按同一条规则挂,两份实现必然漂移(它那边就漂过:往回找
  // 最后一条 assistant,于是三点跳到上一轮的回复上)。判据见包里的注释。
  const lastAssistantId = React.useMemo(
    () => indicatorHostMessageId(messages),
    [messages],
  );

  // 折叠"压缩前"的旧消息:扫所有 messages.blocks,找最后一条 compact_boundary 所在的
  // (messageIdx, blockIdx);该位置之前的所有消息默认隐藏,该消息自己的更早 blocks 也
  // 一并裁掉。expanded=true 时退化为原始 messages 渲染。
  const [expanded, setExpanded] = React.useState(false);
  const fold = React.useMemo(
    () => findLastCompactBoundary(messages),
    [messages],
  );
  const folding = !expanded && fold !== null;
  const foldedCount = folding ? fold.messageIdx : 0;
  const displayMessages = React.useMemo<chat_svc.ChatMessage[]>(() => {
    if (!folding) return messages;
    // 保留 boundary 所在消息及之后;boundary 消息的更早 blocks 裁掉。spread 出来的对象
    // 失去了 wails class 的 convertValues 方法,但下游 MessageItem 只读字段,as 强转
    // 即可,不需要走 Object.assign(Object.create(proto)) 这种重活。
    const out: chat_svc.ChatMessage[] = [];
    for (let i = fold.messageIdx; i < messages.length; i++) {
      if (i === fold.messageIdx && fold.blockIdx > 0) {
        const m = messages[i];
        out.push({
          ...m,
          blocks: (m.blocks ?? []).slice(fold.blockIdx),
        } as chat_svc.ChatMessage);
      } else {
        out.push(messages[i]);
      }
    }
    return out;
  }, [folding, messages, fold]);

  // autonomousIds:自主续轮(CLI 后台任务完成后自主跑的一轮)的消息 id 集合。
  // 判定与它的全部理由归共享包的 transcript-turns 所有 —— 「什么算一轮」在界面上
  // 有两个用处(这里给自主轮挂 banner、「回到底部」药丸报「下面还有 N 轮」),两处
  // 各拼各的必然在自主续轮或旁白行这类边角上分家。
  //
  // 用完整消息表(而非 displayMessages,也不是切掉未取正文那段之后的 messages)算:
  // 判据是「这一轮前面有没有用户消息」,而 compact 折叠与正文窗口都会切掉前缀 ——
  // 切完之后首条 assistant 失去它的"前一条",会被误判成自主轮挂上 banner。
  // 角色序列是元数据,窗口外的消息照样带着。
  const autonomousIds = React.useMemo(
    () => autonomousTurnMessageIds(allMessages),
    [allMessages],
  );

  // displayMessages → 虚拟行。persisted 消息的行缓存在实例级 WeakMap(引用稳定
  // → 行组件 memo 恒命中);live 消息每 chunk 现场重建,重渲上限 = 可见窗口行数。
  const rowsCacheRef = React.useRef(
    new WeakMap<chat_svc.ChatMessage, TranscriptRow[]>(),
  );
  // 本会话的临时本地命令条目(!command),useShallow 浅比身份集合 —— output 流式
  // 追加重建数组但条目身份不变时不触发归并重算。
  const localCommands = useLocalCommandsStore(
    useShallow((s) => s.listForSession(sessionId ?? 0)),
  );
  // R17:非本机发出的用户消息的来源标识。本机指纹与本机消息的 sourceDevice 相等,
  // 全部被 buildSourceByMessageId 跳过 → 单客户端恒为空表,界面零变化。
  const localFingerprint = useLocalDeviceFingerprint();
  const sourceByMessageId = React.useMemo(
    () => buildSourceByMessageId(displayMessages, localFingerprint),
    [displayMessages, localFingerprint],
  );
  // settled:只依赖 messages(流式中引用稳定),整体 memoize —— 每 chunk 不再全量
  // 重建 rows + 两张索引图。live 内容变化时只走 applyLiveTranscriptRows 的 O(live)
  // 叠加(非 live 行与 settled 共享引用,行组件 memo 恒命中)。
  const settled = React.useMemo(
    () =>
      buildSettledTranscriptRows({
        autonomousIds,
        // eslint-disable-next-line react-hooks/refs -- 行缓存是渲染期的旁路:按消息引用做记忆化的 WeakMap,内容由构建函数就地写,React 侧只当它是实例级的稳定容器。这条规则此前不报(forwardRef 包着的渲染函数它认不出是组件),搬进具名 hook 后才开始报 —— 语义与搬家前逐字相同。
        cache: rowsCacheRef.current,
        displayMessages,
        localCommands,
        sourceByMessageId,
      }),
    [autonomousIds, displayMessages, localCommands, sourceByMessageId],
  );
  const { rows, firstRowIndexByMessageId, rowIndexByKey } = React.useMemo(
    () =>
      applyLiveTranscriptRows(settled, {
        autonomousIds,
        // eslint-disable-next-line react-hooks/refs -- 同上:settled 与 live 叠加共用这一个缓存。
        cache: rowsCacheRef.current,
        displayMessages,
        liveByMessageId,
        localCommands,
        sourceByMessageId,
      }),
    [
      autonomousIds,
      displayMessages,
      liveByMessageId,
      localCommands,
      settled,
      sourceByMessageId,
    ],
  );

  return {
    rows,
    firstRowIndexByMessageId,
    rowIndexByKey,
    messages,
    folding,
    foldedCount,
    setExpanded,
    lastAssistantId,
  };
}
