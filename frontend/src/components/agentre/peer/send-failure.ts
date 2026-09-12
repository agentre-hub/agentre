// frontend/src/components/agentre/peer/send-failure.ts
//
// 把「这条消息没发出去」的机器原话解析成一句**事实**。
//
// 原话是英文、来自对端或中继（`agentruntime: no active turn for session`、
// `relay: Agentre App is not running on the target desktop` …）。原样贴给用户等于
// 让他去读我们自己的内部错误串；而一句「发送失败，请重试」又把唯一有用的信息抹掉
// 了 —— 这些失败里大多数重试一百次也不会变。
//
// 所以这里按已知的几种成因分档，各自说一句用户真能据以决定下一步的话；认不出来的
// 那一档如实说「对端没收下」，并把原话作为**证据**留在第二行（它是给排查看的，
// 不是界面说给用户听的那一句）。

export type PeerSendFailure = {
  /** 界面说的那一句事实（i18n key）。 */
  key: string;
  /** 认不出来的原话。只有兜底那一档才有。 */
  detail?: string;
};

export function classifyPeerSendFailure(
  reason: string | undefined,
): PeerSendFailure {
  const raw = (reason ?? "").trim();
  // 「没有正在进行的轮次」：分流之后仍可能撞上 —— 判断到发送之间那一轮正好跑完。
  // 再发一次就会走到 run 那条路上，所以这句话是**可执行**的。
  if (/no active turn/i.test(raw)) return { key: "peerPanel.send.turnEnded" };
  if (/app is not running/i.test(raw)) {
    return { key: "peerPanel.send.appNotRunning" };
  }
  if (/session not found/i.test(raw)) {
    return { key: "peerPanel.send.sessionGone" };
  }
  if (/invalid conversation id/i.test(raw)) {
    return { key: "peerPanel.send.unknownConversation" };
  }
  return { key: "peerPanel.send.rejected", detail: raw || undefined };
}
