package chat_import_svc

import "github.com/agentre-hub/agentre/internal/service/chat_svc"

// types.go 是本服务与 Wails 绑定层之间的 DTO。时间一律 unix 毫秒(与 chat_svc 的
// 会话 / 消息载荷同口径),后端类型与来源标记一律字符串(与实体常量同值)。

// ListCandidatesRequest 发现阶段的过滤条件。
type ListCandidatesRequest struct {
	// DeviceID 是第一维筛选:0 = 本机。远端设备接进来之前,非 0 会如实报「暂不支持」。
	DeviceID int64 `json:"deviceId"`
	// Backends 限定只扫这几个后端;空表示这台设备上注册的全部。
	Backends []string `json:"backends"`
	// CwdPrefix / Since / TitleQuery / Limit 原样交给各后端的 Scan。
	CwdPrefix  string `json:"cwdPrefix"`
	Since      int64  `json:"since"`
	TitleQuery string `json:"titleQuery"`
	Limit      int    `json:"limit"`
}

// CandidateView 是候选列表里的一行。
type CandidateView struct {
	Backend           string `json:"backend"`
	ProviderSessionID string `json:"providerSessionId"`
	Title             string `json:"title"`
	Cwd               string `json:"cwd"`
	StartedAt         int64  `json:"startedAt"`
	EndedAt           int64  `json:"endedAt"`
	// Turns 是轮数;元信息里拿不到时是 0,表示"未知",不要当成"空会话"。
	Turns int `json:"turns"`
	// Origin 是来源标记,只用于展示:"terminal" / "agentre" / ""(认不出就不猜)。
	Origin  string `json:"origin"`
	Locator string `json:"locator"`
	// Imported 为真时这一行不可选:库里已经有同一个 provider_session_id 的会话。
	// 藏起来会让用户以为扫描漏了,所以照常列出并给出「打开」的去处。
	Imported bool `json:"imported"`
	// ImportedSessionID 是那条已在库里的会话 id(Imported 为真时非 0)。
	ImportedSessionID int64 `json:"importedSessionId"`
}

// 发现的非 ok 判别值(spec「远端」的三态;ok 就是「有候选、没 issue」)。
const (
	// ScanStatusUnavailable 这一档此刻答不出:目录读不动、这台机器上没装那个
	// CLI、或者整台设备拨不通。
	ScanStatusUnavailable = "unavailable"
)

// BackendScanIssue 是一档答不出的原因;其余照常出结果。
//
// Backend 为空表示这是**设备级**的一句话(整台设备拨不通):那种情况下三个后端会给出
// 同一个答案,按后端重复三遍只是噪声。
type BackendScanIssue struct {
	Backend string `json:"backend"`
	// Status 目前只有 ScanStatusUnavailable 一档,**永不为空**:
	// 空候选必须自带理由,否则「问出来就是没有」与「答不出」在界面上长得一样。
	Status string `json:"status"`
	Reason string `json:"reason"`
}

// ListCandidatesResponse 合并后的候选 + 各后端的失败。
type ListCandidatesResponse struct {
	Candidates []CandidateView    `json:"candidates"`
	Issues     []BackendScanIssue `json:"issues"`
}

// GapView 是一条缺口声明,UI 拿它渲染导入前的提示。
type GapView struct {
	Kind   string `json:"kind"`
	Count  int    `json:"count"`
	Detail string `json:"detail"`
	// Text 是这条缺口的本机语言说明(与转录内说明块同源)。
	Text string `json:"text"`
}

// TranscriptMetaView 是一份已打开转录的元信息。
type TranscriptMetaView struct {
	Backend           string    `json:"backend"`
	ProviderSessionID string    `json:"providerSessionId"`
	Title             string    `json:"title"`
	Cwd               string    `json:"cwd"`
	Model             string    `json:"model"`
	Turns             int       `json:"turns"`
	ToolCalls         int       `json:"toolCalls"`
	Compactions       int       `json:"compactions"`
	StartedAt         int64     `json:"startedAt"`
	EndedAt           int64     `json:"endedAt"`
	Origin            string    `json:"origin"`
	Gaps              []GapView `json:"gaps"`
	// CwdExists 为假时导入会降级为只读(不写 provider_session_id,续跑关掉)。
	CwdExists bool `json:"cwdExists"`
	// Imported / ImportedSessionID 同 CandidateView。
	Imported          bool  `json:"imported"`
	ImportedSessionID int64 `json:"importedSessionId"`
}

// PreviewRequest 预览一条候选。
type PreviewRequest struct {
	DeviceID int64  `json:"deviceId"`
	Backend  string `json:"backend"`
	Locator  string `json:"locator"`
	// Turns 取前几轮;<=0 用 defaultPreviewTurns。
	Turns int `json:"turns"`
}

// PreviewMessage 是预览里的一条消息。它与导入真正落库的那条**同一条生成路径**
// (同一个 dispatcher + accumulator),只是不落库 —— 预览是回放的前 N 轮,不是
// 另一条解析路径。
type PreviewMessage struct {
	Role       string `json:"role"`
	Seq        int    `json:"seq"`
	Createtime int64  `json:"createtime"`
	Model      string `json:"model"`
	ErrorText  string `json:"errorText"`
	// Blocks 是这条消息投影好的前端块 —— 与 chat_svc.ChatMessage.Blocks 同一条
	// 投影(chat_svc.ProjectBlocks),预览与真实回放/重载渲染同一份形状,而不是
	// 让前端自己解 blocksJson 原文。
	Blocks []chat_svc.ChatBlock `json:"blocks"`
}

// PreviewResponse 预览结果。
type PreviewResponse struct {
	Meta     TranscriptMetaView `json:"meta"`
	Messages []PreviewMessage   `json:"messages"`
	// PreviewedTurns / RemainingTurns 让预览末尾能说清「后面还有多少轮」。
	// RemainingTurns 为 -1 表示元信息里没有轮数,说不出还剩几轮。
	PreviewedTurns int `json:"previewedTurns"`
	RemainingTurns int `json:"remainingTurns"`
}

// ImportRequest 导入一条候选。
type ImportRequest struct {
	DeviceID int64  `json:"deviceId"`
	Backend  string `json:"backend"`
	Locator  string `json:"locator"`
	// AgentID 是续跑要绑的 agent(它带出 backend / provider / model);必填。
	AgentID int64 `json:"agentId"`
	// ProjectID 可为 0(自由会话)。
	ProjectID int64 `json:"projectId"`
	// Cwd 是用户另选的工作目录("选择新目录"那条出口,spec「续跑」)。空 = 用磁盘
	// 转录里记的那个。非空时这条会话**一律只读**:provider session id 是按原目录
	// 记的,claude 的 --resume 换个目录就找不到它(决策 16)——换来的只是"接着聊
	// 时从哪儿起 CLI"有了答案。
	Cwd string `json:"cwd"`
	// RequestID 由前端生成,用来在写库途中通过 Cancel 主动中断这一笔导入
	// (spec「导入过程给出按轮计的进度,可取消」)。留空 = 不可中断,沿用
	// agent_backend_svc.CancelTest 的同一条约定。
	RequestID string `json:"requestId"`
}

// CancelImportRequest 中断一笔还在写的导入。RequestID 必须与发起时一致;
// 未知 id 答 Canceled=false 而不是报错 —— 导入刚返回、取消慢半拍是前端常态。
type CancelImportRequest struct {
	RequestID string `json:"requestId"`
}

// CancelImportResponse 返回是否真的命中了在跑的那一笔。
type CancelImportResponse struct {
	Canceled bool `json:"canceled"`
}

// ImportResponse 导入结果。
type ImportResponse struct {
	SessionID int64 `json:"sessionId"`
	// AlreadyImported 为真时 SessionID 指向库里早就存在的那条,本次一行都没写。
	AlreadyImported bool `json:"alreadyImported"`
	// ReadOnly 为真表示 cwd 已不存在,转录照导但没写 provider_session_id,
	// 这条会话不能接着对话(spec 决策 16)。
	ReadOnly      bool   `json:"readOnly"`
	Cwd           string `json:"cwd"`
	ImportedTurns int    `json:"importedTurns"`
}
