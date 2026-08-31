package remote

// conversation.go 是 remote.Runtime 上「进程内 int64 会话键 ↔ 线上 conversation_id」
// 的**唯一**翻译处。
//
// 线上身份是 conversation_id(uuid 字符串);而本进程内 —— 以及 agentruntime 的整套
// 接口 —— 仍旧按 int64 索引会话(spec 决策 6:那是进程本地的 runtime key,不上线)。
// 两者的桥必须只有一座,否则同一条会话在两处算出两个不同的值,推送就再也落不到轮次上。
//
// 桥的方向:
//   - 出线:conversationID(sid)。取的是这条会话建档时就落库的那个身份(经注入的
//     ConversationIDResolver 读 chat_sessions.conversation_id),线格式上因此与库里
//     逐字相同。
//   - 入线:localSessionID(cid)。只认之前登记过的对话;没登记过的说明本进程从未为它
//     发过请求,按未知会话丢弃(与今天收到陌生 sid 的行为一致)。

// ConversationIDResolver 交出一条本地会话在线上的身份 —— 也就是
// chat_sessions.conversation_id 那一列。
//
// agentruntime 不认识仓储层,所以这个值由装配方注入(见
// chat_svc/remote_pool.go)。它必须**读那一列**,不能按 (指纹, 会话 id) 现算:
// 新对话的号是建档那一刻铸出来的 UUIDv7,派生算出的是另一个值,推送就再也落不
// 回这条会话。
type ConversationIDResolver func(sessionID int64) string

// WithConversationIDResolver 注入上面那条翻译。不注入时 conversationID 交回空串
// —— 那正是「此刻没人能说出这条会话在线上叫什么」的真实情况,由调用方按缺身份处理。
func WithConversationIDResolver(resolve ConversationIDResolver) Option {
	return func(r *Runtime) { r.conversations = resolve }
}

// conversationID 交出这条本地会话在线上的身份。
//
// 查库只做一次,之后原样从表里取:这一列建档时写一次就不再改,而这条路上每一次
// 出线请求都要它。登记的同时建反向映射,让 daemon 推上来的帧翻得回这条会话。
func (r *Runtime) conversationID(sid int64) string {
	r.convMu.Lock()
	if cid, ok := r.convBySid[sid]; ok {
		r.convMu.Unlock()
		return cid
	}
	r.convMu.Unlock()

	if r.conversations == nil {
		return ""
	}
	cid := r.conversations(sid)
	if cid == "" {
		return ""
	}
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
