package transcript

// source.go 把「提交方的设备身份」盖进一条用户消息的正文块。
//
// 它是**两个宿主共用的那一份**:agentred 与桌面端都要为「别人发过来的那一句」留下
// 来源标识,而这份标识骑在文本块的 data 里(不是消息级的一列)—— 块→帧投影原样把它
// 带给消费方,消费方据它渲染来源 pill。两边各写一份就会在键名、覆盖时机或「盖第几个
// 块」上分叉,而分叉的表现是同一句话在两台宿主上渲染出不同的来源(规格 2026-09-05
// 决策 2:一份投影,一份实现)。

import (
	"encoding/json"
	"fmt"

	"github.com/cago-frame/agents/agent/blocks"
)

// UserSource 是一条用户消息的**提交方设备身份**。空值(Device 为空)表示本机自己发的
// 那一句 —— 它不盖任何来源,与「没有来源」是同一种字节。
//
// 它是宿主之间传这两格的那一份形状:agentred 从 runtime.run 的 SourceDevice /
// SourceDeviceName 取,桌面端从中继连接认下的对端身份取,最后都交给
// StampUserMessageSource 盖进同一个位置。
type UserSource struct {
	Device string
	Name   string
}

// StampUserMessageSource 把 device / name 盖进 blocksJSON 里**第一个**文本块的 data,
// 交回改写后的 JSON。
//
// 只盖第一个:一条用户消息的来源是整条的属性,重复盖在每个块上只会让载荷变大、并给
// 「块之间来源不一致」留下表达空间。device 为空(本机发的)时原样交回,不做任何改写 ——
// 空来源和「没有来源」必须是同一种字节,否则同一句话在两台宿主上投影出不同的帧。
//
// name 为空只盖设备指纹:名字是可选的展示信息,拿不到时消费方回退成指纹。
func StampUserMessageSource(blocksJSON, device, name string) (string, error) {
	if device == "" {
		return blocksJSON, nil
	}
	var stored []blocks.StoredBlock
	if err := json.Unmarshal([]byte(blocksJSON), &stored); err != nil {
		return "", fmt.Errorf("decode user message source: %w", err)
	}
	for index := range stored {
		if stored[index].Type != "text" && stored[index].Type != "display_text" {
			continue
		}
		var data map[string]json.RawMessage
		if err := json.Unmarshal(stored[index].Data, &data); err != nil {
			return "", fmt.Errorf("decode user text source: %w", err)
		}
		encodedDevice, _ := json.Marshal(device)
		data["sourceDevice"] = encodedDevice
		if name != "" {
			encodedName, _ := json.Marshal(name)
			data["sourceDeviceName"] = encodedName
		}
		encoded, err := json.Marshal(data)
		if err != nil {
			return "", fmt.Errorf("encode user text source: %w", err)
		}
		stored[index].Data = encoded
		all, err := json.Marshal(stored)
		if err != nil {
			return "", fmt.Errorf("encode user message source: %w", err)
		}
		return string(all), nil
	}
	return blocksJSON, nil
}
