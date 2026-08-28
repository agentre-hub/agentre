import type { TranscriptMessage } from "./dto";

/**
 * isNoticeOnlyMessage 判断一条消息是不是「只承载供应商切换 notice 的旁白行」。
 *
 * 切换 notice 是独立落库的一条消息(桌面端 session_provider.go 的
 * appendProviderSwitchNotice):role 是 assistant、块只有一个 notice,但它不是一轮
 * 对话 —— 它可以在任意时刻插进 transcript(pill 允许轮中切换,切完立刻 reloadSession
 * 把它拉进来),包括插在在跑的 assistant 之后。
 *
 * 所以凡是「找末条**真实** assistant」的推导都必须跳过它,否则它会顶替真正那一条:
 * 生成指示器跳到旁白行上,在跑的那条看着像已经停了。
 *
 * 判据是 noticeKind==="switch",而不是「块全是 notice」:回退 notice 由后端追加进
 * **这一轮自己**的 assistant 消息,零内容收尾时那条消息的块正好只剩它 —— 按「块全是
 * notice」判,一轮真实对话就会被当成旁白行跳过。与桌面端 chat.go 的
 * noticeOnlyMessage 同一口径,两边必须同时改。
 *
 * 没有块 ≠ 旁白行:轮刚起时 assistant 行的 blocks 恒为 `[]`,那是真实的一轮,
 * 判定必须认到它(指示器就该在那一刻出现)。
 */
export function isNoticeOnlyMessage(
  message: Pick<TranscriptMessage, "blocks">,
): boolean {
  const blocks = message.blocks ?? [];
  return (
    blocks.length > 0 &&
    blocks.every((b) => b.type === "notice" && b.noticeKind === "switch")
  );
}

/**
 * indicatorHostMessageId:生成指示器(三个点)挂在哪一条消息上。没有合适的宿主时
 * 返回 null —— 此时**谁都不挂**,宁可不出指示器。
 *
 * 规则是「只看末条」,不是「从后往前找最后一条 assistant」。两者的差别只在末条是
 * 用户消息的时候,而那正是最要紧的一刻:用户刚发出去、对端一个字都还没回。往回找
 * 会把三点挂到**上一轮**的回复上 —— 用户刚说的那句话下面空着,而上面那段早就说完
 * 的回复看着像还在写。
 *
 * 末条是用户消息时该出现的是新一轮的 assistant 占位:桌面端在 doSend 里与用户消息
 * 同时插入,agentre-server 由转录组件按 `pendingAssistant` 合成。占位一旦在,末条
 * 就是 assistant,本函数自然挂到它上面。
 *
 * 只承载 notice 的旁白行在这里透明(见 isNoticeOnlyMessage)。
 */
export function indicatorHostMessageId(
  messages: readonly TranscriptMessage[],
): number | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const message = messages[i];
    if (isNoticeOnlyMessage(message)) continue;
    return message.role === "assistant" ? message.id : null;
  }
  return null;
}
