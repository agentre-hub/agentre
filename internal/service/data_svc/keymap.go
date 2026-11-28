package data_svc

// keyMap 把 bundle 内部的 stable key（providerKey / instanceUUID / exportKey）
// 映射到 import 完成后的本地 row ID,供下游引用解析使用。
type keyMap struct {
	providers map[string]int64
	devices   map[string]int64
	backends  map[string]int64
	depts     map[string]int64
	agents    map[string]int64
	engines   map[string]int64
}

func newKeyMap() *keyMap {
	return &keyMap{
		providers: make(map[string]int64),
		devices:   make(map[string]int64),
		backends:  make(map[string]int64),
		depts:     make(map[string]int64),
		agents:    make(map[string]int64),
		engines:   make(map[string]int64),
	}
}
