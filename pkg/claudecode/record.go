package claudecode

// RecordDecoder 把 claude CLI 落在磁盘上的会话记录（`~/.claude/projects/<slug>/<sid>.jsonl`
// 的一行）解成 Event。
//
// 为什么是同一套解码逻辑:磁盘那份与 stdout 的 stream-json 是同一个协议的两次序列化,
// 内层 message 完全一致,差别只在顶层若干字段的拼法(见 rawFrame 里成对的
// snake / camel 字段)。实测 200 个真实文件 / 96 699 行,这套解码只在
// `system/api_error` 那一种行上失败过 —— 那处冲突已修。重写一份 claude 解析器等于
// 把已经验证过的工具调用配对逻辑再写一遍。
//
// 与 Stream 的差别:Stream 自己持有 stdout 的 Scanner 并按 turn 收口,而磁盘回放的
// 分轮、线性化(沿 parentUuid 回溯叶子链)由调用方负责,所以这里只暴露"喂一行、拿事件"。
// 逐行喂而不是接 io.Reader,正是为了让调用方能先选出叶子链再解、且不必把整份转录
// 攒进内存。
//
// 状态是有的(与 frameDecoder 同一份):session id / model / 增量正文账本随行推进,
// 所以**一份转录用一个 RecordDecoder**,不要跨文件复用。子代理文件另起一个。
type RecordDecoder struct {
	d *frameDecoder
}

// NewRecordDecoder 建一个空解码器。
func NewRecordDecoder() *RecordDecoder {
	return &RecordDecoder{d: &frameDecoder{}}
}

// Decode 解一行记录。
//
// ok=false 表示这一行是坏数据(部分写入 / 未知形状),调用方跳过该行并计入缺口即可 ——
// 解码器状态不受影响,后续行照常解。ok=true 而事件为空是正常的:attachment、
// last-prompt、纯 user prompt 等记录本就不产生事件。
func (r *RecordDecoder) Decode(line []byte) ([]Event, bool) {
	if len(line) == 0 {
		return nil, true
	}
	return r.d.decodeLine(line)
}

// SessionID 返回目前为止从记录里读到的 session id;磁盘方言不带该字段时为空串。
func (r *RecordDecoder) SessionID() string { return r.d.SessionID() }
