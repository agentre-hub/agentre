package agenttool

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
)

// Ref 是内置工具 MCP token 绑定的 (agent, session) —— 也是本 server 唯一的授权依据:
// 工具调用只对令牌里的这一对生效,请求参数里自称的 agent/session 一律不作数。
type Ref struct {
	AgentID   int64
	SessionID int64
}

// TokenSigner 签发 / 校验绑定 (agent, session) 的无状态 HMAC token。内置工具 server
// 与 agrctl 的会话级 token(ctl_svc)共用这一种签名方式;密钥是 per-instance 的,
// 所以签发与校验必须用同一个实例,一个实例签的 token 在另一个实例上验不过。
type TokenSigner struct {
	secret []byte
}

// NewTokenSigner 造一个自持随机密钥的签名器。
func NewTokenSigner() *TokenSigner { return &TokenSigner{secret: randSecret()} }

// MintToken 为某 (agent, session) 签一个无状态签名 token:
// `b64url(agent:session).b64url(HMAC(secret, agent:session))`。
//
// token 在 CLI spawn 时注入、复用轮不重发,因此必须与子进程同寿命:
// 同一 (agent, session) 每次返回相同值(确定性)。
func (s *TokenSigner) MintToken(agentID, sessionID int64) string {
	payload := strconv.FormatInt(agentID, 10) + ":" + strconv.FormatInt(sessionID, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." + s.sign(payload)
}

func (s *TokenSigner) sign(payload string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Lookup 验签并解出 token 绑定的 (agent, session)。仅做密码学校验(无状态);验签失败 /
// 格式非法 → !ok。密钥是 per-instance 的,因此另一个签名器签的 token 在这里验不过
// —— 这是「持 A 资源的令牌操作不了 B 资源」的唯一屏障。
func (s *TokenSigner) Lookup(tok string) (Ref, bool) {
	payloadB64, sig, ok := strings.Cut(tok, ".")
	if !ok {
		return Ref{}, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil || !hmac.Equal([]byte(s.sign(string(payload))), []byte(sig)) {
		return Ref{}, false
	}
	aStr, sStr, ok := strings.Cut(string(payload), ":")
	if !ok {
		return Ref{}, false
	}
	agentID, err1 := strconv.ParseInt(aStr, 10, 64)
	sessionID, err2 := strconv.ParseInt(sStr, 10, 64)
	if err1 != nil || err2 != nil {
		return Ref{}, false
	}
	return Ref{AgentID: agentID, SessionID: sessionID}, true
}

// randSecret 生成本 server 的 HMAC 签名密钥(32 字节)。crypto/rand 失败是不可恢复的灾难;
// 签名密钥绝不能退化为可预测值, 必须 fail loud。
func randSecret() []byte {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("agenttool: crypto/rand failed: " + err.Error())
	}
	return b
}
