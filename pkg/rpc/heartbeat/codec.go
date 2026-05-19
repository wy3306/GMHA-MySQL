package heartbeat

import "encoding/json"

// JSONCodec 实现了基于 JSON 格式的编解码器，用于心跳消息的序列化和反序列化。
type JSONCodec struct{}

// Name 返回编解码器的名称标识。
func (JSONCodec) Name() string {
	return "json"
}

// Marshal 将任意类型的值序列化为 JSON 字节数组。
func (JSONCodec) Marshal(v any) ([]byte, error) {
	return json.Marshal(v)
}

// Unmarshal 将 JSON 字节数组反序列化为指定类型的值。
func (JSONCodec) Unmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}
