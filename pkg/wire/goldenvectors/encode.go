package goldenvectors

import (
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Binary 一条向量落盘的二进制形态。
//
// Deterministic 只为让「重新生成 → 逐字节比对」这条守卫成立(map 字段否则每次序不同),
// 它是**生成器这一侧**的性质,不是对消费方的要求:向量的判据从来不是字节相等。
func Binary(v Vector) ([]byte, error) {
	b, err := proto.MarshalOptions{Deterministic: true}.Marshal(v.Message)
	if err != nil {
		return nil, fmt.Errorf("向量 %s 序列化失败: %w", v.Name, err)
	}
	return b, nil
}

// ExpectedJSON 一条向量的期望字段值,以 protobuf 的规范 JSON 形态写下。
//
// 它是 Swift 侧的第二条独立取材:Swift 解 .bin 与解同名 .json 必须得到相等的消息。
// 两条路径在 SwiftProtobuf 里是两套实现(二进制解析器与 JSON 解析器),同时对上才说明
// 这批字段真的被读对了,而不是两边犯了同一个错。
//
// protojson 的输出**不稳定**(它会随机多插一个空格,专为防止有人拿字节做比较),
// 因此这里过一遍 encoding/json 归一化:map 键排序、缩进固定,守卫才比得下去。
func ExpectedJSON(v Vector) ([]byte, error) {
	raw, err := protojson.Marshal(v.Message)
	if err != nil {
		return nil, fmt.Errorf("向量 %s 转 JSON 失败: %w", v.Name, err)
	}
	var normalized any
	if err := json.Unmarshal(raw, &normalized); err != nil {
		return nil, fmt.Errorf("向量 %s 的 JSON 不合法: %w", v.Name, err)
	}
	out, err := json.MarshalIndent(normalized, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("向量 %s 归一化失败: %w", v.Name, err)
	}
	return append(out, '\n'), nil
}
