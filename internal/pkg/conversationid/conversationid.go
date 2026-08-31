// Package conversationid 拥有 conversation_id —— 一条对话在桌面端、agentred 与
// server 三套库以及线格式上的**唯一身份**。
//
// 两种铸法,混用无碍(两者都是 UUID,只有版本位不同):
//
//   - New() 铸 UUIDv7,给**新建**的对话。发起端在建档那一刻铸,无 I/O、无网络,
//     因此未登录 / 离线也建得起对话。
//   - Derive() 按 UUIDv5 派生,给**存量**对话回填。桌面端与 server 各持一份存量、
//     迁移时互不通信,只有确定性派生才能让两边独立算出同一个值。
//
// 本包在三个仓库里各有一份**逐字相同**的拷贝(agentre / agentre-server /
// agentre-hub)。Namespace 与 Derive 的拼接方式是跨仓库契约:改动其中任何一处,
// 三边就会给同一条对话算出不同的 uuid,镜像存量全体成孤儿。
package conversationid

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// Namespace 是派生存量 conversation_id 用的 UUIDv5 命名空间,三个仓库共用同一个值。
//
// 取值可复算:UUIDv5(uuid.NameSpaceURL, "https://agentre.dev/ns/conversation")。
// 之所以钉成字面量而不是每次现算,是因为它是跨仓库、跨版本的常量 —— 字面量能被
// 逐字比对,现算的表达式一旦在某个仓库里被改写就无声地分了家。
var Namespace = uuid.MustParse("44d41290-935a-525a-853c-81d0e171598e")

// ErrInvalid 表示这个字符串不是一个可用作 conversation_id 的规范 uuid。
var ErrInvalid = errors.New("conversationid: not a canonical conversation id")

// New 铸一条新对话的 conversation_id(UUIDv7)。
//
// v7 的前 48 位是毫秒时间戳,因此新铸的 id 在索引里天然近似有序;剩余位取自
// crypto/rand,唯一性无需任何协调。错误只可能来自系统熵源不可用。
func New() (string, error) {
	id, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("conversationid.New: %w", err)
	}
	return id.String(), nil
}

// Derive 按 (对端指纹, 对端会话 id) 确定性地派生一条**存量**对话的 conversation_id。
//
// 输入是这条对话的**发起端指纹** —— 也就是发起端向执行端出示、并被落进
// peer_fingerprint 那一列的值,不是执行端 daemon 自己的实例指纹;取错则两边算出
// 不同的 uuid。两段之间垫一个 NUL:少了它,("ab","1") 与 ("a","b1") 会撞成同一条。
//
// 同一组输入永远得到同一个输出,因此回填可以重跑、可以分批、可以在三个仓库里各跑一遍。
func Derive(namespace uuid.UUID, peerFingerprint, peerSessionID string) string {
	name := make([]byte, 0, len(peerFingerprint)+1+len(peerSessionID))
	name = append(name, peerFingerprint...)
	name = append(name, 0)
	name = append(name, peerSessionID...)
	return uuid.NewSHA1(namespace, name).String()
}

// Validate 判定一个线上取值能否用作 conversation_id。
//
// 只接受**规范形式**:36 字符、小写、带连字符、非全零。uuid.Parse 本身还认花括号 /
// urn: / 无连字符三种变体与大写,放行它们等于让同一条对话有多种写法,而这个值要当
// 数据库主键和路由键用。全零 uuid 能解析但不指称任何对话,一律视为"没给"。
func Validate(id string) error {
	parsed, err := uuid.Parse(id)
	if err != nil {
		return fmt.Errorf("%w: %q", ErrInvalid, id)
	}
	if parsed == uuid.Nil || parsed.String() != id {
		return fmt.Errorf("%w: %q", ErrInvalid, id)
	}
	return nil
}
