package wire

import (
	"fmt"
	"strconv"
)

// SessionListMaxLimit 是 session.list 一页的上限。对端一律收口到它:分页是为了让
// 「机器轴一打开就把整台机器搬回来」不再发生,一个手写的大 limit 不该把这件事绕过去。
// 与桌面端索引那条路的 listAgentSessionsMaxLimit 同值,两处翻页的口径因此一致。
const SessionListMaxLimit = 100

// SessionListMaxIDs 是一次按对话身份收窄最多点名几条。
//
// 比一页的上限宽:点名是调用方拿着自己手上那份名单来问的(账号保存的那些、屏幕上
// 那一条),它本来就有界;而收窄之后对端读的是主键点查,不是扫表。仍然设上限,是因为
// 一个无界的 IN 列表会把 SQL 撑成另一种全表扫描。超过的由调用方分批。
const SessionListMaxIDs = 200

// ClampSessionListLimit 收口一页的大小。
//
// **0 及以下原样留 0**,那是协议里「不分页」那一档:不带 limit 的老客户端要的就是
// 整份清单,把它改写成默认页大小会让那些客户端只看得见最前面几条。
func ClampSessionListLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit > SessionListMaxLimit {
		return SessionListMaxLimit
	}
	return limit
}

// EncodeSessionListCursor 把一页的起点编成游标。
//
// 游标对调用方**不透明**(协议里写明了):今天它就是偏移量的十进制写法,来日换成
// keyset 时调用方一行都不用改。所以两端都只经这一对函数进出,不在别处解析它。
func EncodeSessionListCursor(offset int) string {
	return strconv.Itoa(offset)
}

// DecodeSessionListCursor 解回一页的起点。空串 = 从最新那条开始(第一页)。
//
// 解不动时**报错**而不是退回 0:那是调用方参数错了,悄悄从头开始会让翻到一半的
// 弹层重复列出前几条,看起来像会话被复制了。
func DecodeSessionListCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	offset, err := strconv.Atoi(cursor)
	if err != nil || offset < 0 {
		return 0, fmt.Errorf("invalid session list cursor %q", cursor)
	}
	return offset, nil
}
