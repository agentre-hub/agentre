package paths

// SetBuildChannelForTest 在测试期间替换 ldflags 注入的构建标记，并在测试结束时恢复。
// 只供其他包的测试证明「非法标记拒绝启动」等行为；生产代码不得调用。
func SetBuildChannelForTest(tb interface{ Cleanup(func()) }, value string) {
	prev := buildChannel
	buildChannel = value
	tb.Cleanup(func() { buildChannel = prev })
}
