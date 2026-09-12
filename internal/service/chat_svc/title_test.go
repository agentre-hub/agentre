package chat_svc

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSessionTitleFromFirstMessagePreservesTextBeyondVisualClamp(t *testing.T) {
	text := "Optimize Edit/Write/file_change so the frontend owns visual truncation"
	require.Greater(t, utf8.RuneCountInString(text), 30)

	got := sessionTitleFromFirstMessage("  " + text + "  ")

	assert.Equal(t, text, got)
	assert.NotContains(t, got, "\u2026")
}

func TestSessionTitleFromFirstMessageDoesNotApplyRenameLimit(t *testing.T) {
	text := strings.Repeat("x", renameTitleMaxRunes+1)

	got := sessionTitleFromFirstMessage(text)

	assert.Equal(t, text, got)
	assert.NotContains(t, got, "\u2026")
}

func TestSessionTitleFromFirstMessageRendersMentionXmlAsReadableLabel(t *testing.T) {
	cases := []struct {
		name string
		text string
		want string
	}{
		{
			name: "agent tag becomes @label",
			text: "<agent id=\"1\">CEO \u52a9\u624b</agent>",
			want: "@CEO \u52a9\u624b",
		},
		{
			name: "project tag with path attr becomes @label",
			text: `<project id="2" path="/Users/me/web">Web</project>`,
			want: "@Web",
		},
		{
			name: "device tag with fp attr becomes @label",
			text: `<device fp="sha256:ab12">工作站</device>`,
			want: `@工作站`,
		},
		{
			name: "surrounding text is preserved",
			text: "ping <agent id=\"1\">CEO \u52a9\u624b</agent> now",
			want: "ping @CEO \u52a9\u624b now",
		},
		{
			name: "escaped label is unescaped for display",
			text: `<agent id="1">a &amp; b</agent>`,
			want: "@a & b",
		},
		{
			name: "multiple tags all rendered",
			text: `<agent id="1">A</agent> and <project id="2" path="/p">B</project>`,
			want: "@A and @B",
		},
		{
			name: "no tags leaves text unchanged aside from trim",
			text: "  plain text with no mentions  ",
			want: "plain text with no mentions",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := sessionTitleFromFirstMessage(tc.text)
			assert.Equal(t, tc.want, got)
		})
	}
}
