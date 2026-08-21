package syncwire

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// ErrPayloadLocalID 表示载荷里出现了本地自增 ID。
var ErrPayloadLocalID = errors.New("sync payload carries a local auto-increment id")

// ErrPayloadCredential 表示载荷里出现了凭据或 provider 行正文。
var ErrPayloadCredential = errors.New("sync payload carries a credential or a provider row")

// ErrPayloadNotObject 表示载荷不是一个 JSON 对象。
var ErrPayloadNotObject = errors.New("sync payload must be a json object")

// ErrPayloadAvatarContent 表示载荷里出现了头像正文而不是内容哈希（R16a）。
var ErrPayloadAvatarContent = errors.New("sync payload carries avatar content instead of a content hash")

// GuardPayload 是上行前的载荷守卫。kind 让唯一可携带 API Key 的
// llm_provider 与所有其它对象明确分开；调用者不可省略它。它按**键名**挡住三类东西：
//
//   - 本地自增 ID（键名以 id 结尾、取值是数字）。跨机引用一律用同步标识、agentred
//     指纹或 provider_key 表达，它们全是字符串；这个形状只可能是本机主键，在别的
//     机器上指向完全不同的对象。
//   - `api_key` 这个键，以及整行 provider 正文（`provider` / `providers` 取值是
//     对象或数组）。只有 kind=llm_provider 能携带 api_key；其它对象只传
//     provider_key 这个字符串。agent_backend 身份载荷也不得含 cli_path。
//   - `avatar_data_url`：头像正文按内容哈希单独传，不进同步载荷。
//
// **它挡不住、也没打算挡的：载荷里以 JSON 字符串形式携带的正文。** 最要紧的一处是
// agentBackendPayload.EnvJSON——它是用户自填的透传环境变量表，是 backend 配置的一
// 部分，按设计随 backend 上行，在账号下明文存放。里面不会有 App 自管的那 15 个保留
// 键（ANTHROPIC_API_KEY / OPENAI_API_KEY 等，由 agent_backend_entity.Check 拒绝入
// 库），但用户自己往里放的任何别的密钥都会原样过机。
//
// 这条守卫不是凭据扫描器，别把它当成一个——要挡住 env_json 就得整个字段不上行，
// 那是规格决定，不是守卫能顺手做掉的事。
//
// server 侧有一份同规则的守卫（sync_entity.ValidatePayload），两边规则必须一致：
// 两个仓库不能互相 import，只能靠这两段注释与各自的共享测试向量对齐。server 那份
// 保护账号里的其它设备，这一份保证坏载荷根本发不出去。规则不一致时坏的一边只会让
// 单条被拒（server 回 rejected，本端出队并进 R5 列表），不会堵住整条队列。
//
// 空载荷合法：墓碑不带正文。
func GuardPayload(kind string, payload []byte) error {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var root any
	if err := dec.Decode(&root); err != nil {
		return fmt.Errorf("decode sync payload: %w", err)
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return ErrPayloadNotObject
	}
	return walkPayload(kind, obj)
}

func walkPayload(kind string, node any) error {
	switch t := node.(type) {
	case map[string]any:
		for key, value := range t {
			if err := checkPayloadKey(kind, key, value); err != nil {
				return err
			}
			if err := walkPayload(kind, value); err != nil {
				return err
			}
		}
	case []any:
		for _, value := range t {
			if err := walkPayload(kind, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkPayloadKey(kind, key string, value any) error {
	norm := normalizePayloadKey(key)
	if norm == "apikey" && kind != KindLLMProvider {
		return ErrPayloadCredential
	}
	if norm == "clipath" && kind == KindAgentBackend {
		return ErrPayloadCredential
	}
	// 头像正文只按内容哈希单独传（R16a）；这个键名一旦出现在载荷里，不论值是
	// 什么，都说明有代码路径想把正文塞进同步载荷——直接挡住，不看值的内容。
	if norm == "avatardataurl" {
		return ErrPayloadAvatarContent
	}
	if norm == "provider" || norm == "providers" {
		// provider_key 归一化后是 providerkey，不落进这一条：字符串引用照常放行，
		// 被挡住的是整行 provider 正文。
		switch value.(type) {
		case map[string]any, []any:
			return ErrPayloadCredential
		}
	}
	if strings.HasSuffix(norm, "id") {
		if _, isNumber := value.(json.Number); isNumber {
			return ErrPayloadLocalID
		}
	}
	return nil
}

// normalizePayloadKey 把 agent_backend_id / agentBackendId / agent-backend-id 归一成
// 同一个形状，免得换个命名风格就绕过守卫。
func normalizePayloadKey(key string) string {
	var b strings.Builder
	b.Grow(len(key))
	for _, r := range strings.ToLower(key) {
		if r == '_' || r == '-' || r == ' ' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}
