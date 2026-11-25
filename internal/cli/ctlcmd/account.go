package ctlcmd

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/agentre-hub/agentre/internal/buildinfo"
	"github.com/agentre-hub/agentre/internal/pkg/agrctlcred"
	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/internal/pkg/paths"
	"github.com/agentre-hub/agentre/pkg/wire/devicefp"
)

// loginPollWait 是 Device Flow 两次轮询之间的停顿；测试换成不睡眠的。
var loginPollWait = func(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

const (
	defaultLoginPollInterval = 5 * time.Second
	slowDownIncrement        = 5 * time.Second
)

var loginFlags = []*flagDef{
	{name: "server", value: "<url>", usage: "account server (default: " + ctlclient.DefaultServerURL + ", or AGENTRE_SERVER_URL)"},
}

// credentialsDir 是 agrctl 的登录凭据所在目录：按构建渠道隔离的 AppDataDir。
func credentialsDir() (string, error) { return paths.AppDataDir() }

// runLogin：`agrctl login [--server <url>]`，走 Device Flow 把本机 agrctl 登记成账号下的一台
// `cli` 设备。
func runLogin(args []string, s *sys) error {
	p, err := parseArgs(args, loginFlags)
	if err != nil {
		return err
	}
	if len(p.positional) > 0 {
		return usageErrorf("login takes no arguments")
	}
	dir, err := credentialsDir()
	if err != nil {
		return err
	}
	prev, err := agrctlcred.Load(dir)
	switch {
	case err == nil && !prev.Expired():
		return fmt.Errorf("already logged in as %s — run 'agrctl logout' first", prev.Account)
	case err != nil && !errors.Is(err, os.ErrNotExist):
		return err
	}
	server, err := loginServer(s, last(p, "server"), prev)
	if err != nil {
		return err
	}

	ctx := context.Background()
	hostname, _ := os.Hostname() // 取不到时空串上报，server 回退到指纹缩写
	fp := devicefp.FromSeed("agrctl:" + randomSeed())
	auth, err := ctlclient.AuthorizeDevice(ctx, server, ctlclient.DeviceLoginRequest{
		Fingerprint: string(fp), Platform: runtime.GOOS, Version: agrctlVersion(), Name: hostname,
	})
	if err != nil {
		return err
	}
	verify := auth.VerificationURIComplete
	if verify == "" {
		verify = auth.VerificationURI
	}
	_, _ = fmt.Fprintf(s.stderr, "To log agrctl in, open %s\nand confirm the code %s.\nWaiting for approval …\n", verify, auth.UserCode)

	tokens, err := pollLogin(ctx, server, auth)
	if err != nil {
		return err
	}
	me, err := ctlclient.FetchIdentity(ctx, server, tokens.AccessToken)
	if err != nil {
		return err
	}
	now := time.Now()
	c := &agrctlcred.Credentials{
		ServerURL: server, AccountID: me.UserID, Account: me.Label(),
		DeviceID: tokens.DeviceID, DeviceName: hostname, Fingerprint: string(fp),
		RefreshToken: tokens.RefreshToken, AccessToken: tokens.AccessToken,
		AccessExpiresAt: now.Add(time.Duration(tokens.ExpiresIn) * time.Second).Unix(),
	}
	if tokens.RefreshExpiresIn > 0 {
		c.RefreshExpiresAt = now.Add(time.Duration(tokens.RefreshExpiresIn) * time.Second).Unix()
	}
	if err := agrctlcred.Save(dir, c); err != nil {
		return err
	}
	_, _ = fmt.Fprintf(s.stdout, "logged in as %s — this machine's agrctl is now a device of the account\n", c.Account)
	return nil
}

// loginServer：--server > AGENTRE_SERVER_URL > 作废登录的 server > 默认。只接受 http(s) base URL。
func loginServer(s *sys, flag string, prev *agrctlcred.Credentials) (string, error) {
	raw := strings.TrimSpace(flag)
	if raw == "" {
		if v, ok := s.lookupEnv("AGENTRE_SERVER_URL"); ok {
			raw = strings.TrimSpace(v)
		}
	}
	if raw == "" && prev != nil {
		raw = prev.ServerURL
	}
	if raw == "" {
		raw = ctlclient.DefaultServerURL
	}
	raw = strings.TrimRight(raw, "/")
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", usageErrorf("--server must be an http(s) URL, got %q", raw)
	}
	return raw, nil
}

// pollLogin 按 server 给的间隔轮询，直到批准、被拒或过期。
func pollLogin(ctx context.Context, server string, auth *ctlclient.DeviceAuthorization) (*ctlclient.DeviceTokens, error) {
	interval := time.Duration(auth.Interval) * time.Second
	if interval <= 0 {
		interval = defaultLoginPollInterval
	}
	deadline := time.Now().Add(time.Duration(auth.ExpiresIn) * time.Second)
	for {
		if time.Now().After(deadline) {
			return nil, ctlclient.ErrAuthorizationExpired
		}
		if err := loginPollWait(ctx, interval); err != nil {
			return nil, err
		}
		tokens, err := ctlclient.PollDeviceToken(ctx, server, auth.DeviceCode)
		switch {
		case err == nil:
			return tokens, nil
		case errors.Is(err, ctlclient.ErrAuthorizationPending):
		case errors.Is(err, ctlclient.ErrSlowDown):
			interval += slowDownIncrement
		default:
			return nil, err
		}
	}
}

// randomSeed 是本次登录设备指纹的种子：每次登录一台新的 `cli` 设备。
func randomSeed() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func agrctlVersion() string {
	if c := buildinfo.ShortCommitID(); c != "" {
		return "agrctl " + c
	}
	return "agrctl"
}

// runLogout：先在 server 吊销本 `cli` 设备，再删本地凭据；server 不可达时仍删本地并提示。
func runLogout(args []string, s *sys) error {
	if len(args) > 0 {
		return usageErrorf("logout takes no arguments")
	}
	dir, err := credentialsDir()
	if err != nil {
		return err
	}
	c, err := agrctlcred.Load(dir)
	if errors.Is(err, os.ErrNotExist) {
		return errors.New("not logged in")
	}
	if err != nil {
		return err
	}
	var revokeErr error
	if !c.Expired() {
		acct, err := ctlclient.LoginAccount(dir)
		if err != nil {
			return err
		}
		revokeErr = acct.Revoke(context.Background(), c.DeviceID)
		if errors.Is(revokeErr, ctlclient.ErrLoginExpired) {
			revokeErr = nil // server 已经不认这份登录：没有可吊销的了
		}
	}
	if err := agrctlcred.Delete(dir); err != nil {
		return err
	}
	if revokeErr != nil {
		_, _ = fmt.Fprintf(s.stderr, "Warning: server-side revocation did not complete (%v); "+
			"delete this device (%s) from the account's device list\n", revokeErr, deviceLabel(c))
	}
	_, _ = fmt.Fprintf(s.stdout, "logged out of %s\n", c.Account)
	return nil
}

func deviceLabel(c *agrctlcred.Credentials) string {
	if c.DeviceName != "" {
		return c.DeviceName
	}
	return fmt.Sprintf("#%d", c.DeviceID)
}

// runWhoami：显示 server、账号、本设备名与本次选中的执行者；从不显示凭据字符。
func runWhoami(args []string, s *sys) error {
	if len(args) > 0 {
		return usageErrorf("whoami takes no arguments")
	}
	x, err := selectExecutor(s, "", "")
	var unavailable *defaultDesktopUnavailable
	executorLine := ""
	switch {
	case errors.As(err, &unavailable):
		x, executorLine = &executor{kind: execDesktop}, executorLabel(&executor{kind: execDesktop})+" (not reachable: "+err.Error()+")"
	case err != nil:
		return err
	default:
		executorLine = executorLabel(x)
	}
	var login *agrctlcred.Credentials
	if dir, err := credentialsDir(); err == nil {
		if c, err := agrctlcred.Load(dir); err == nil {
			login = c
		}
	}
	tw := tabwriter.NewWriter(s.stdout, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintf(tw, "executor\t%s\n", executorLine)
	switch {
	case x.kind == execAccount && x.acct.PersonalToken():
		_, _ = fmt.Fprintf(tw, "server\t%s\n", x.acct.Server)
	case login != nil:
		state := ""
		if login.Expired() {
			state = " (login expired — run 'agrctl login' again)"
		}
		_, _ = fmt.Fprintf(tw, "server\t%s\n", login.ServerURL)
		_, _ = fmt.Fprintf(tw, "account\t%s%s\n", login.Account, state)
		_, _ = fmt.Fprintf(tw, "device\t%s (cli)\n", deviceLabel(login))
	default:
		_, _ = fmt.Fprintln(tw, "account\tnot logged in")
	}
	return tw.Flush()
}

func executorLabel(x *executor) string {
	switch x.kind {
	case execSession:
		return "session — this Agentre session"
	case execAccount:
		if x.acct.PersonalToken() {
			return "account — personal token"
		}
		return "account — logged in"
	default:
		return "desktop — the Agentre desktop on this machine"
	}
}
