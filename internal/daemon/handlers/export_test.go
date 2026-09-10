package handlers

// RuntimeSessionKey 把线上的 conversation_id 折成 backend 会话键,只给同目录的
// handlers_test 外部测试包用:那些用例断言的正是"daemon 交给 backend 的是折算过的键"。
// 把折算算法抄一份进测试会让它变成恒真命题,所以这里透出生产实现本身。
func RuntimeSessionKey(conversationID string) int64 { return runtimeSessionID(conversationID) }
