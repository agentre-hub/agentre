package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/agentre-ai/agentre/internal/daemon/state"
	"github.com/agentre-ai/agentre/internal/pkg/paths"
)

type unclaimDeps struct {
	dataDir       func() (string, error)
	daemonRunning func() bool
	http          loginHTTPDoer
}

func newUnclaimCmd() *cobra.Command {
	return newUnclaimCmdWithDeps(unclaimDeps{
		dataDir:       paths.AgentredDataDir,
		daemonRunning: daemonIsRunning,
		http:          &http.Client{Timeout: 15 * time.Second},
	})
}

func newUnclaimCmdWithDeps(deps unclaimDeps) *cobra.Command {
	return &cobra.Command{
		Use:   "unclaim",
		Short: "Remove this daemon's local account claim",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireNoRunningDaemon(deps.daemonRunning); err != nil {
				return err
			}
			dir, err := deps.dataDir()
			if err != nil {
				return err
			}
			st, err := state.Load(dir)
			if err != nil {
				return err
			}
			// 先尽力通知账号侧解除授权，再清本地——顺序不可换，Unclaim 会把凭据一并抹掉。
			//
			// 这一步是 best-effort：失败只写一行提示，绝不阻挡本地清理。用户执行了
			// unclaim 就必须解除归属，否则一台再也连不上账号的机器将永远回不到未认领
			// 状态，也就永远无法重新配对或登录另一个账号（前置规格 R19）。
			snapshot := st.Snapshot()
			if err := revokeAccountAuthorization(cmd, deps.http, snapshot.HubServerURL, snapshot.Credential); err != nil {
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(),
					"Could not tell the account server to revoke this device (revoke it from the device list instead): %v\n", err)
			}
			st.Unclaim()
			if err := st.Save(); err != nil {
				return fmt.Errorf("save unclaimed state: %w", err)
			}
			_, _ = fmt.Fprintln(cmd.OutOrStdout(), "Daemon account claim removed.")
			return nil
		},
	}
}

// revokeAccountAuthorization 调账号侧的「解除授权」，与桌面端 Logout 同构：两条路径
// 都打到 POST /v1/oauth/token/revoke，因此撤销 token 链、拉黑 jti、以及把只属于这台
// 机器的账号级同步对象落墓碑这三件事只有一份实现，不会两处漂移。
//
// 撤销目标由凭据本身认定（设备 JWT 调用方只能撤自己），不带 device_id 点名。
func revokeAccountAuthorization(
	cmd *cobra.Command, client loginHTTPDoer, serverURL string, credential state.AccountCredential,
) error {
	if client == nil || serverURL == "" {
		// 从没登录过账号（只做过 LAN 配对）：没有账号侧可通知，unclaim 仍是纯本地操作。
		return nil
	}
	token := credential.AccessToken
	if credential.RefreshToken != "" {
		// access token 是分钟级的，而 unclaim 的前置条件正是「daemon 已经停了」——
		// 那份存盘的 access token 在真实场景里几乎总是过期的，必须先换一张新的。
		// 换不到就退回手上这张：它也许还没过期，让撤销那一步去给出真正的结论。
		fresh, err := refreshAccessTokenForRevoke(cmd, client, serverURL, credential.RefreshToken)
		switch {
		case err == nil:
			token = fresh
		case token == "":
			return err
		}
	}
	if token == "" {
		return nil
	}
	return postRevoke(cmd, client, serverURL, token)
}

// refreshAccessTokenForRevoke 用刷新令牌换一张新的 access token。这里刻意不落盘：
// 换来的这张只服务紧接着的那一次撤销，而 state.json 下一步就要被 Unclaim 清空。
func refreshAccessTokenForRevoke(
	cmd *cobra.Command, client loginHTTPDoer, serverURL, refreshToken string,
) (string, error) {
	var token deviceTokenResponse
	oauthErr, err := doUnclaimJSON(cmd, client, serverURL+"/v1/oauth/token/refresh", "",
		map[string]string{"refresh_token": refreshToken}, &token)
	if err != nil {
		return "", err
	}
	if oauthErr != nil {
		return "", fmt.Errorf("refresh rejected: %s: %s", oauthErr.Code, oauthErr.Description)
	}
	if token.AccessToken == "" {
		return "", fmt.Errorf("refresh endpoint returned an invalid token payload")
	}
	return token.AccessToken, nil
}

func postRevoke(cmd *cobra.Command, client loginHTTPDoer, serverURL, accessToken string) error {
	oauthErr, err := doUnclaimJSON(cmd, client, serverURL+"/v1/oauth/token/revoke", accessToken,
		map[string]any{}, nil)
	if err != nil {
		return err
	}
	if oauthErr != nil {
		return fmt.Errorf("revoke rejected: %s: %s", oauthErr.Code, oauthErr.Description)
	}
	return nil
}

// doUnclaimJSON 是 doLoginJSON 的带鉴权变体：同样兼容裸载荷与 cago 的 {data: …} 响应壳，
// 只多一个 Authorization 头。两者不合并——登录那条路径按约定从不带凭据。
func doUnclaimJSON(
	cmd *cobra.Command, client loginHTTPDoer, endpoint, accessToken string,
	requestBody any, responseBody any,
) (*oauthErrorResponse, error) {
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(cmd.Context(), http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if accessToken != "" {
		req.Header.Set("Authorization", "Bearer "+accessToken)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	payload, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		oauthErr := oauthErrorResponse{}
		if err := decodeLoginResponse(payload, &oauthErr); err == nil && oauthErr.Code != "" {
			return &oauthErr, nil
		}
		return nil, fmt.Errorf("server returned %s: %s", resp.Status, strings.TrimSpace(string(payload)))
	}
	if responseBody == nil {
		return nil, nil
	}
	if err := decodeLoginResponse(payload, responseBody); err != nil {
		return nil, err
	}
	return nil, nil
}
