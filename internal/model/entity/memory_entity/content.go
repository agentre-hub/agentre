package memory_entity

import (
	"context"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/agentre-hub/agentre/internal/pkg/code"
)

// credentialPattern 明显的凭据格式：私钥头与常见令牌前缀。这只是底线检查——前缀后
// 要跟足够长的令牌体才算，免得「sk-learn」这类普通词被误拦。
var credentialPattern = regexp.MustCompile(
	`-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----` +
		`|\bsk-[A-Za-z0-9_-]{20,}` + // OpenAI / Anthropic
		`|\bgh[pousr]_[A-Za-z0-9]{30,}` + // GitHub
		`|\bgithub_pat_[A-Za-z0-9_]{20,}` +
		`|\bglpat-[A-Za-z0-9_-]{20,}` + // GitLab
		`|\bxox[abprs]-[A-Za-z0-9-]{10,}` + // Slack
		`|\bAKIA[0-9A-Z]{16}\b` + // AWS access key id
		`|\bAIza[0-9A-Za-z_-]{35}`, // Google API key
)

// NormalizeContent 在写入边界统一去掉摘要与详情的首尾空白，并把详情里的 Windows 换行
// （\r\n）规范成 \n——否则孤立的 \r 会被当成不可见字符拒掉。
func NormalizeContent(summary, detail string) (string, string) {
	return strings.TrimSpace(summary), strings.TrimSpace(strings.ReplaceAll(detail, "\r\n", "\n"))
}

// summaryLineBreaks 是会把摘要断成多行的字符：换行、回车与 Unicode 的行 / 段分隔符。
const summaryLineBreaks = "\r\n  "

// CheckContent 校验已规范化的摘要与详情：长度上限、摘要单行、不可见字符、凭据格式。
// 拒绝时的错误码各自说明原因与上限。
func CheckContent(ctx context.Context, summary, detail string) error {
	if summary == "" {
		return i18n.NewError(ctx, code.MemorySummaryRequired)
	}
	if utf8.RuneCountInString(summary) > SummaryMaxChars {
		return i18n.NewError(ctx, code.MemorySummaryTooLong)
	}
	if strings.ContainsAny(summary, summaryLineBreaks) {
		return i18n.NewError(ctx, code.MemorySummaryMultiline)
	}
	if utf8.RuneCountInString(detail) > DetailMaxChars {
		return i18n.NewError(ctx, code.MemoryDetailTooLong)
	}
	if hasInvisible(summary) || hasInvisible(detail) {
		return i18n.NewError(ctx, code.MemoryInvisibleChars)
	}
	if credentialPattern.MatchString(summary) || credentialPattern.MatchString(detail) {
		return i18n.NewError(ctx, code.MemorySensitiveContent)
	}
	return nil
}

// hasInvisible 报告是否含有不可见字符：格式类字符（零宽、方向控制、BOM、软连字符等，
// Unicode Cf）与除换行、制表符以外的控制字符（Cc），以及非法 UTF-8。
func hasInvisible(s string) bool {
	if !utf8.ValidString(s) {
		return true
	}
	for _, r := range s {
		if r == '\n' || r == '\t' {
			continue
		}
		if unicode.Is(unicode.Cf, r) || unicode.IsControl(r) {
			return true
		}
	}
	return false
}
