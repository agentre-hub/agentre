// Package cagoenvelope 解 cago 的 {data: ...} 响应外壳。agentre 不 import
// agentre-server,登录流程、daemon 的 refresh 与 introspect、engine snapshot 都靠
// 这一份 helper 保持同一条「有 data 解 data、否则当裸 JSON」的契约。
package cagoenvelope

import "encoding/json"

// Decode 把 payload 解进 target:优先 cago 的 {data: ...} 外壳;没有 data(或 data 为
// null)时按裸 JSON 解。
func Decode(payload []byte, target any) error {
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return err
	}
	if len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		return json.Unmarshal(envelope.Data, target)
	}
	return json.Unmarshal(payload, target)
}
