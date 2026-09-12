package transcript

// userblocks.go 回答「runtime.run 带来的 userBlocks 里,哪些是这一轮的**附件**」。
//
// 这一格的两个调用方填法本就不同,而宿主只有一份落库逻辑:
//   - 桌面端的 chat_svc.buildRunRequest 交出的是那条用户消息的**全部**块
//     (agentruntime.RunRequest.UserBlocks 的约定:非空即本轮用户输入的权威 blocks),
//     所以里面已经含着与 userText 同一句话的那个文本块;
//   - 浏览器控制台只放它发的图(agentre-server 的 useSessionSend.encodeUserBlocks),
//     文本只在 userText 里。
//
// 宿主若把 userText 与 userBlocks 直接相加,前一种就会把同一句话落成两个文本块 ——
// 转录里读作这句话说了两遍,还凭空多占一个持久帧位。两个宿主都从这里过一道,落下的
// 用户消息因此都是「一个文本块 + 这些附件」,与桌面端本机发图那条路
// (chat_svc.userBlocksForSend)同形。

import cagoblocks "github.com/cago-frame/agents/agent/blocks"

// TurnAttachments 交出这一轮真正的附件:userBlocks 里**第一个**与 userText 同字的
// 文本块是 userText 自己的另一种写法,去掉它;其余块连同顺序原样保留。
//
// userText 为空(自主续轮、或只发了图)时不去掉任何东西 —— 那时 userBlocks 是这条
// 消息仅有的正文来源。
func TurnAttachments(userText string, userBlocks []cagoblocks.ContentBlock) []cagoblocks.ContentBlock {
	if userText == "" || len(userBlocks) == 0 {
		return userBlocks
	}
	for index, block := range userBlocks {
		if text, ok := plainTextOf(block); !ok || text != userText {
			continue
		}
		out := make([]cagoblocks.ContentBlock, 0, len(userBlocks)-1)
		out = append(out, userBlocks[:index]...)
		return append(out, userBlocks[index+1:]...)
	}
	return userBlocks
}

// plainTextOf 读出一个文本块的正文。值与指针两种写法都认:块从 wire 解出来是值
// (blocks.RegisterFactory[TextBlock]),桌面端自己组的是指针(userBlocksForSend)。
func plainTextOf(block cagoblocks.ContentBlock) (string, bool) {
	switch text := block.(type) {
	case cagoblocks.TextBlock:
		return text.Text, true
	case *cagoblocks.TextBlock:
		if text == nil {
			return "", false
		}
		return text.Text, true
	}
	return "", false
}
