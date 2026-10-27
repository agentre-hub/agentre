package chat_entity

// ValidCLIModelChoice 校验三态的完整组合，不接受带残留值的默认态。
func ValidCLIModelChoice(mode, value string) bool {
	switch mode {
	case "", "cli-default":
		return value == ""
	case "fixed":
		return value != ""
	default:
		return false
	}
}
