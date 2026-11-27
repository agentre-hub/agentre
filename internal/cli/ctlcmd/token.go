package ctlcmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/agentre-hub/agentre/internal/pkg/cagoenvelope"
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
)

// 个人 token 管理（spec「个人 token 与范围」）：create / rotate / list / delete token。只有
// 已登录且由人在终端里操作才行——个人 token 不能创建 token，登录态加外部程序也被客户端护栏拒绝
// （决策 17）。token 明文只在创建 / 轮换那一次打印，list 从不显示 token 字符。

const (
	//nolint:gosec // G101: usage text, not a credential.
	tokenCreateUsage = `agrctl create token --name <name> --scope <s>[,<s>...] [--expires <duration>|never]

Creates a personal token on the account server for a program that must drive agrctl without a
person: set it as AGENTRE_TOKEN (or pass --token). Needs agrctl login and a person at a terminal.
Scopes: read, chat, approve, resources:write, devices:write. --expires takes a duration such as
30m, 12h or 90d (default 30d) or "never". The secret is printed once and cannot be shown again.`
	//nolint:gosec // G101: usage text, not a credential.
	tokenRotateUsage = `agrctl rotate token <name> [--expires <duration>|never]

Replaces the secret of the named token: name, scopes and the sessions it started stay, the old
secret stops working at once (open relay connections within 60 seconds). Without --expires the
old lifetime rule restarts from now. The new secret is printed once. Needs agrctl login and a
person at a terminal.`
	//nolint:gosec // G101: usage text, not a credential.
	tokenListUsage = `agrctl list tokens [-o json]

Lists the account's personal tokens: name, scopes, created, expires (or "never"), last used, last
rotated and the device that created it. Never shows token characters. Needs agrctl login and a
person at a terminal.`
	//nolint:gosec // G101: usage text, not a credential.
	tokenDeleteUsage = `agrctl delete token <name>

Revokes the named personal token. Needs agrctl login and a person at a terminal.`
)

// tokenUsage 是 help 里关于 token 管理的全部说明。
const tokenUsage = tokenCreateUsage + "\n\n" + tokenRotateUsage + "\n\n" + tokenListUsage + "\n\n" + tokenDeleteUsage

var (
	tokenNameFlag    = &flagDef{name: "name", value: "<name>", usage: "token name, unique in the account"}
	tokenScopeFlag   = &flagDef{name: "scope", value: "<s>[,<s>...]", usage: "granted scopes", repeat: true}
	tokenExpiresFlag = &flagDef{name: "expires", value: "<duration>|never", usage: "lifetime"}
)

// tokenVerb 报告这条命令是不是 token 管理，并交回动作；rest 是动词之后的参数。
func tokenVerb(verb string, rest []string) (string, bool) {
	if len(rest) == 0 {
		return "", false
	}
	switch {
	case verb == "list" && (rest[0] == "tokens" || rest[0] == "token"):
		return "list", true
	case (verb == "create" || verb == "delete" || verb == "rotate") && (rest[0] == "token" || rest[0] == "tokens"):
		return verb, true
	}
	return "", false
}

func tokenHelp(action string) string {
	switch action {
	case "create":
		return tokenCreateUsage
	case "rotate":
		return tokenRotateUsage
	case "delete":
		return tokenDeleteUsage
	}
	return tokenListUsage
}

// runToken 执行一条 token 管理命令；args 是资源名（token / tokens）之后的参数。
func runToken(action string, args []string, s *sys) error {
	if wantsHelp(args) {
		_, _ = fmt.Fprintln(s.stdout, tokenHelp(action))
		return nil
	}
	var (
		defs     []*flagDef
		wantName bool
	)
	switch action {
	case "create":
		defs = []*flagDef{tokenNameFlag, tokenScopeFlag, tokenExpiresFlag}
	case "rotate":
		defs, wantName = []*flagDef{tokenExpiresFlag}, true
	case "delete":
		wantName = true
	default:
		defs = outputFlagNames
	}
	p, err := parseArgs(args, defs)
	if err != nil {
		return err
	}
	name := ""
	switch {
	case wantName && len(p.positional) != 1:
		return usageErrorf("%s token needs exactly one <name>", action)
	case wantName:
		name = strings.TrimSpace(p.positional[0])
	case len(p.positional) > 0:
		return usageErrorf("unexpected argument %q", p.positional[0])
	}
	var body map[string]any
	switch action {
	case "create":
		if name = strings.TrimSpace(last(p, "name")); name == "" {
			return usageErrorf("create token: --name is required")
		}
		scopes := splitScopes(p.values("scope"))
		if len(scopes) == 0 {
			return usageErrorf("create token: --scope is required (read, chat, approve, resources:write, devices:write)")
		}
		body = map[string]any{"name": name, "scopes": scopes}
	case "rotate":
		body = map[string]any{}
	}
	never := false
	if body != nil && p.has("expires") {
		var expires map[string]any
		if expires, never, err = parseExpires(last(p, "expires")); err != nil {
			return err
		}
		for k, v := range expires {
			body[k] = v
		}
	}
	asJSON := false
	if action == "list" {
		if asJSON, err = wantJSON(p); err != nil {
			return err
		}
	}

	acct, err := humanLoginAccount(s)
	if err != nil {
		return err
	}
	ctx := context.Background()
	switch action {
	case "list":
		return listTokens(ctx, s, acct, asJSON)
	case "delete":
		if _, err := acct.DeleteRaw(ctx, "/v1/personal-tokens/"+url.PathEscape(name)); err != nil {
			return serverErr(err)
		}
		_, _ = fmt.Fprintf(s.stdout, "deleted token %s\n", name)
		return nil
	}
	path := "/v1/personal-tokens"
	if action == "rotate" {
		path += "/" + url.PathEscape(name) + "/rotate"
	}
	payload, _ := json.Marshal(body)
	raw, err := acct.PostRaw(ctx, path, payload)
	if err != nil {
		return serverErr(err)
	}
	var out struct {
		Token string `json:"token"`
	}
	if err := cagoenvelope.Decode(raw, &out); err != nil || out.Token == "" {
		return errors.New("the account server returned no token")
	}
	if never {
		_, _ = fmt.Fprintln(s.stderr, "Warning: this token does not expire — delete it when it is no longer needed")
	}
	_, _ = fmt.Fprintln(s.stderr, "Copy the token now; it is shown only this once:")
	_, _ = fmt.Fprintln(s.stdout, out.Token)
	return nil
}

// humanLoginAccount 取管理 token 要用的账号执行者：本机登录态、人在终端。个人 token 与桌面、
// 会话都不行。
func humanLoginAccount(s *sys) (*ctlclient.Account, error) {
	x, err := selectExecutor(s, "", "")
	var unavailable *defaultDesktopUnavailable
	switch {
	case errors.As(err, &unavailable):
		return nil, errNeedsLogin
	case err != nil:
		return nil, err
	case x.kind == execAccount && x.acct.PersonalToken():
		return nil, errors.New("a personal token cannot manage tokens — run 'agrctl login' and manage them at your own terminal")
	case x.kind != execAccount:
		return nil, errNeedsLogin
	}
	if err := x.refuseExternal(s, "managing personal tokens"); err != nil {
		return nil, err
	}
	return x.acct, nil
}

var errNeedsLogin = errors.New("managing personal tokens needs an account login — run 'agrctl login'")

// splitScopes 把可重复、逗号分隔的 --scope 摊平成列表。
func splitScopes(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, sc := range strings.Split(v, ",") {
			if sc = strings.TrimSpace(sc); sc != "" {
				out = append(out, sc)
			}
		}
	}
	return out
}

// parseExpires 把 --expires 换成请求字段：never → no_expiry；其余是正的时长（支持 d 天）→
// expires_in 秒。
func parseExpires(v string) (map[string]any, bool, error) {
	v = strings.TrimSpace(v)
	if v == "never" {
		return map[string]any{"no_expiry": true}, true, nil
	}
	d, err := parseLifetime(v)
	if err != nil || d <= 0 {
		return nil, false, usageErrorf("--expires: %q is not a positive duration (e.g. 30m, 12h, 90d) or \"never\"", v)
	}
	return map[string]any{"expires_in": int64(d / time.Second)}, false, nil
}

func parseLifetime(v string) (time.Duration, error) {
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.ParseFloat(days, 64)
		if err != nil {
			return 0, err
		}
		return time.Duration(n * float64(24*time.Hour)), nil
	}
	return time.ParseDuration(v)
}

type tokenItem struct {
	Name                string   `json:"name"`
	Scopes              []string `json:"scopes"`
	CreatedAt           int64    `json:"created_at"`
	ExpiresAt           int64    `json:"expires_at"`
	LastUsedAt          int64    `json:"last_used_at"`
	RotatedAt           int64    `json:"rotated_at"`
	CreatedByDeviceName string   `json:"created_by_device_name"`
}

func listTokens(ctx context.Context, s *sys, acct *ctlclient.Account, asJSON bool) error {
	raw, err := acct.GetRaw(ctx, "/v1/personal-tokens")
	if err != nil {
		return serverErr(err)
	}
	var out struct {
		Tokens []tokenItem `json:"tokens"`
	}
	if err := cagoenvelope.Decode(raw, &out); err != nil {
		return fmt.Errorf("decode the token list: %w", err)
	}
	if asJSON {
		if out.Tokens == nil {
			out.Tokens = []tokenItem{}
		}
		b, _ := json.MarshalIndent(out.Tokens, "", "  ")
		_, _ = fmt.Fprintln(s.stdout, string(b))
		return nil
	}
	tw := tabwriter.NewWriter(s.stdout, 0, 0, 3, ' ', 0)
	_, _ = fmt.Fprintln(tw, "NAME\tSCOPES\tCREATED\tEXPIRES\tLAST USED\tLAST ROTATED\tCREATED ON")
	for _, t := range out.Tokens {
		expires := "never"
		if t.ExpiresAt != 0 {
			expires = tokenTime(t.ExpiresAt)
		}
		created := t.CreatedByDeviceName
		if created == "" {
			created = "-"
		}
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", t.Name, strings.Join(t.Scopes, ","),
			tokenTime(t.CreatedAt), expires, tokenTime(t.LastUsedAt), tokenTime(t.RotatedAt), created)
	}
	return tw.Flush()
}

// tokenTime 是 unix 毫秒的本地时间；0 = 没有这回事，写 -。
func tokenTime(ms int64) string {
	if ms == 0 {
		return "-"
	}
	return time.UnixMilli(ms).Local().Format("2006-01-02 15:04")
}
