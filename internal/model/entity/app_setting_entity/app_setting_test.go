package app_setting_entity

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

type validationCase struct {
	name    string
	input   string
	wantErr bool
}

func assertValidationCases(t *testing.T, cases []validationCase, validate func(context.Context, string) error) {
	t.Helper()
	ctx := context.Background()
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(ctx, tc.input)
			if tc.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestIsProxyKey(t *testing.T) {
	cases := []struct {
		name string
		s    *AppSetting
		want bool
	}{
		{"nil", nil, false},
		{"proxy host", &AppSetting{Key: KeyProxyListenHost}, true},
		{"proxy port", &AppSetting{Key: KeyProxyListenPort}, true},
		{"other proxy.*", &AppSetting{Key: "proxy.tls"}, true},
		{"unrelated", &AppSetting{Key: "theme.dark"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.s.IsProxyKey())
		})
	}
}

func TestValidateProxyHost(t *testing.T) {
	assertValidationCases(t, []validationCase{
		{"loopback", "127.0.0.1", false},
		{"any", "0.0.0.0", false},
		{"ipv6 loopback", "::1", false},
		{"with whitespace", "  127.0.0.1  ", false},
		{"empty", "", true},
		{"whitespace only", "   ", true},
		{"hostname", "localhost", true},
		{"garbage", "not-an-ip", true},
	}, ValidateProxyHost)
}

func TestValidateProxyPort(t *testing.T) {
	assertValidationCases(t, []validationCase{
		{"zero (random)", "0", false},
		{"valid", "60080", false},
		{"max", "65535", false},
		{"negative", "-1", true},
		{"overflow", "65536", true},
		{"non-numeric", "abc", true},
		{"empty", "", true},
		{"with whitespace", "  60080  ", false},
	}, ValidateProxyPort)
}

func TestParseProxyPort(t *testing.T) {
	cases := []struct {
		input string
		want  int
	}{
		{"0", 0},
		{"60080", 60080},
		{"65535", 65535},
		{"65536", 0},
		{"-1", 0},
		{"abc", 0},
		{"", 0},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			assert.Equal(t, tc.want, ParseProxyPort(tc.input))
		})
	}
}

func TestValidateBoolSetting(t *testing.T) {
	ctx := context.Background()
	for _, ok := range []string{"true", "false", " true "} {
		assert.NoError(t, ValidateBoolSetting(ctx, ok), "input=%q", ok)
	}
	for _, bad := range []string{"", "1", "yes", "True", "maybe"} {
		assert.Error(t, ValidateBoolSetting(ctx, bad), "input=%q", bad)
	}
}

func TestValidateFileOpenAction(t *testing.T) {
	ctx := context.Background()
	for _, ok := range []string{"preview", "external", " external "} {
		assert.NoError(t, ValidateFileOpenAction(ctx, ok), "input=%q", ok)
	}
	// 空串也非法：这个键没有「撤销」语义，缺省由前端在读不到键时决定。
	for _, bad := range []string{"", "Preview", "system", "true", "open"} {
		assert.Error(t, ValidateFileOpenAction(ctx, bad), "input=%q", bad)
	}
}

func TestParseBoolSetting(t *testing.T) {
	assert.True(t, ParseBoolSetting("true"))
	for _, f := range []string{"false", "", "1", "x"} {
		assert.False(t, ParseBoolSetting(f), "input=%q", f)
	}
}
