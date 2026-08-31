package remote

import (
	"strconv"

	"github.com/agentre-hub/agentre/internal/pkg/conversationid"
)

// conversation.go 是 remote.Runtime 上「进程内 int64 会话键 ↔ 线上 conversation_id」
// 的**唯一**翻译处。
//
// 线上身份是 conversation_id(uuid 字符串);而本进程内 —— 以及 agentruntime 的整套
// 接口 —— 仍旧按 int64 索引会话(spec 决策 6:那是进程本地的 runtime key,不上线)。
// 两者的桥必须只有一座,否则同一条会话在两处算出两个不同的值,推送就再也落不到轮次上。
//
// 桥的方向:
//   - 出线:conversationID(sid)。本端自己发起的会话由 conversationid.Derive 确定性
//     算出 —— 输入正是**日后迁移回填要用的那一对**(发起端指纹, 本端会话 id),两处
//     因此逐位相同,不是过渡占位值。
//   - 入线:localSessionID(cid)。只认之前登记过的对话;没登记过的说明本进程从未为它
//     发过请求,按未知会话丢弃(与今天收到陌生 sid 的行为一致)。

// conversationID 交出这条本地会话在线上的身份。
//
// 派生只做一次,之后原样从表里取:输入里的本端指纹取自当前连接,而重连会换连接 ——
// 每次现算的话,一条跨过重连的会话会在中途换一个身份,它的推送就再也落不回来了。
// 登记的同时建反向映射,让 daemon 推上来的帧翻得回这条会话。
func (r *Runtime) conversationID(sid int64) string {
	r.convMu.Lock()
	if cid, ok := r.convBySid[sid]; ok {
		r.convMu.Unlock()
		return cid
	}
	r.convMu.Unlock()

	cid := conversationid.Derive(conversationid.Namespace, r.selfFingerprint(), strconv.FormatInt(sid, 10))
	r.rememberConversation(sid, cid)
	return cid
}

// localSessionID 把线上的 conversation_id 翻回进程内的会话键。未登记返 0 ——
// 调用方按「不认识这条会话」处理。
func (r *Runtime) localSessionID(cid string) int64 {
	if cid == "" {
		return 0
	}
	r.convMu.Lock()
	defer r.convMu.Unlock()
	return r.sidByConv[cid]
}

// rememberConversation 登记一条对话身份的双向映射。
func (r *Runtime) rememberConversation(sid int64, cid string) {
	if sid == 0 || cid == "" {
		return
	}
	r.convMu.Lock()
	defer r.convMu.Unlock()
	if r.convBySid == nil {
		r.convBySid = map[int64]string{}
	}
	if r.sidByConv == nil {
		r.sidByConv = map[string]int64{}
	}
	r.convBySid[sid] = cid
	r.sidByConv[cid] = sid
}

// selfFingerprint 本端在当前这条连接的握手里出示过的设备指纹。
// 没有连接(已 Close)时是空串,那正是「此刻没有对端身份」的真实情况。
func (r *Runtime) selfFingerprint() string {
	conn := r.conn()
	if conn == nil {
		return ""
	}
	return conn.SelfFingerprint()
}
