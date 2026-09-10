package peer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agentre-hub/agentre/internal/daemon/auth"
	"github.com/agentre-hub/agentre/internal/service/server_svc"
)

// errNotSignedIn 是「本进程此刻验不了任何凭据」:没登录就没有账号可比、也没有
// server 地址可取钥。它与「凭据不合法」在握手上同一处置(拒绝),分开只为日志说得清。
var errNotSignedIn = errors.New("peer: desktop is not signed in to an account")

// accountVerificationKeyTTL 是公钥集在本地的新鲜期。过期只影响「要不要顺手刷一次」,
// 不影响已缓存的钥继续验:server 够不着时桌面端仍按上一次拿到的钥判定(与 agentred
// 的 R3 同一取向)。
const accountVerificationKeyTTL = 10 * time.Minute

// accountVerificationRefreshFloor 是两次抓公钥之间的最小间隔。没有它,一串验不过的
// 凭据就能把桌面端变成 server /v1/keys 的压测客户端。
const accountVerificationRefreshFloor = time.Minute

// accountCredentialVerifier 在桌面端这一侧兑现决策 8:入站对端出示的账号凭据先按
// server 的 RS256 公钥集验签、比账号,再从中取出对端身份(pfp claim)。
//
// 验证本体与 agentred 共用 auth.VerifyAccountCredential —— 两个 Mode C 入口对
// 「什么算验过了」必须只有一个答案。差别只在材料从哪来:agentred 有轮询下来的缓存
// 快照,桌面端没有,所以这里自己抓 /v1/keys 并缓存。
//
// **已知边界**:桌面端没有吊销列表的本地副本(agentred 的 R4 轮询是 daemon 侧的),
// 因此这条路上的吊销只由凭据自身的过期时间兜住。这是明说的取舍,不是遗漏:补上它
// 需要桌面端也常驻一份吊销缓存,属于另一件事。
type accountCredentialVerifier struct {
	// account 交出本机登录的 server 地址与账号 id。生产是 server_svc 的单行状态。
	account func(ctx context.Context) (serverURL string, accountID string, err error)
	client  *http.Client
	now     func() time.Time

	mu        sync.Mutex
	keys      auth.KeySet
	fetchedAt time.Time
}

func newAccountCredentialVerifier(account func(context.Context) (string, string, error)) *accountCredentialVerifier {
	return &accountCredentialVerifier{
		account: account,
		client:  &http.Client{Timeout: 10 * time.Second},
		now:     time.Now,
	}
}

// Verify 交出凭据里那个已验签的对端身份。任何一步不成立都返回错误,调用方一律以
// unauthorized 拒绝握手 —— 绝不退回去采信请求体。
func (v *accountCredentialVerifier) Verify(ctx context.Context, credential string) (string, error) {
	serverURL, accountID, err := v.account(ctx)
	if err != nil {
		return "", err
	}
	if serverURL == "" || accountID == "" {
		return "", errNotSignedIn
	}
	keys, err := v.keySet(ctx, serverURL)
	if err != nil {
		return "", err
	}
	verified, err := auth.VerifyAccountCredential(credential, keys)
	if err != nil {
		// 验不过也可能是 server 刚轮换了钥:补抓一次(有频率下限)再判一次。
		refreshed, refreshErr := v.refresh(ctx, serverURL)
		if refreshErr != nil {
			return "", err
		}
		verified, err = auth.VerifyAccountCredential(credential, refreshed)
		if err != nil {
			return "", err
		}
	}
	// 同一账号才是对端。跨账号的凭据即使签名成立也不是这台桌面端的对端。
	if verified.AccountID != accountID {
		return "", fmt.Errorf("peer: credential belongs to account %s, not %s", verified.AccountID, accountID)
	}
	return verified.PeerFingerprint, nil
}

func (v *accountCredentialVerifier) keySet(ctx context.Context, serverURL string) (auth.KeySet, error) {
	v.mu.Lock()
	cached, fetchedAt := v.keys, v.fetchedAt
	v.mu.Unlock()
	if len(cached.ByKID) != 0 && v.now().Sub(fetchedAt) < accountVerificationKeyTTL {
		return cached, nil
	}
	refreshed, err := v.refresh(ctx, serverURL)
	if err != nil {
		if len(cached.ByKID) != 0 {
			// server 够不着时继续用上一次拿到的钥:握手不该因为 server 抖动而失败。
			return cached, nil
		}
		return auth.KeySet{}, err
	}
	return refreshed, nil
}

// refresh 抓一次 /v1/keys。两次抓取之间有频率下限,越过下限时交出当前缓存
// (缓存为空则如实报错)。
func (v *accountCredentialVerifier) refresh(ctx context.Context, serverURL string) (auth.KeySet, error) {
	v.mu.Lock()
	if !v.fetchedAt.IsZero() && v.now().Sub(v.fetchedAt) < accountVerificationRefreshFloor {
		cached := v.keys
		v.mu.Unlock()
		if len(cached.ByKID) == 0 {
			return auth.KeySet{}, errors.New("peer: verification keys unavailable")
		}
		return cached, nil
	}
	v.mu.Unlock()

	set, err := fetchVerificationKeys(ctx, v.client, serverURL)
	if err != nil {
		return auth.KeySet{}, err
	}
	v.mu.Lock()
	v.keys, v.fetchedAt = set, v.now()
	v.mu.Unlock()
	return set, nil
}

type verificationKeysPayload struct {
	CurrentKID              string            `json:"current_kid"`
	Keys                    map[string]string `json:"keys"`
	MaxTokenLifetimeSeconds int64             `json:"max_token_lifetime_seconds"`
}

func fetchVerificationKeys(ctx context.Context, client *http.Client, serverURL string) (auth.KeySet, error) {
	endpoint := strings.TrimRight(serverURL, "/") + "/v1/keys"
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return auth.KeySet{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return auth.KeySet{}, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return auth.KeySet{}, err
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return auth.KeySet{}, fmt.Errorf("peer: verification keys endpoint returned %s", response.Status)
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	payload := body
	if json.Unmarshal(body, &envelope) == nil && len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		payload = envelope.Data
	}
	var keys verificationKeysPayload
	if err := json.Unmarshal(payload, &keys); err != nil {
		return auth.KeySet{}, err
	}
	if keys.CurrentKID == "" || keys.Keys[keys.CurrentKID] == "" || keys.MaxTokenLifetimeSeconds <= 0 {
		return auth.KeySet{}, errors.New("peer: verification keys endpoint returned an invalid key set")
	}
	return auth.KeySet{
		CurrentPEM:  keys.Keys[keys.CurrentKID],
		ByKID:       keys.Keys,
		MaxLifetime: time.Duration(keys.MaxTokenLifetimeSeconds) * time.Second,
	}, nil
}

// serverAccountState 是生产的账号来源:桌面端联机状态那一行。
func serverAccountState(ctx context.Context) (string, string, error) {
	svc := server_svc.Server()
	if svc == nil {
		return "", "", errNotSignedIn
	}
	state, err := svc.GetState(ctx)
	if err != nil {
		return "", "", err
	}
	if state == nil || !state.IsLoggedIn() {
		return "", "", errNotSignedIn
	}
	return state.ServerURL, strconv.FormatInt(state.ServerUserID, 10), nil
}

// verifyInboundAccountCredential 是生产装配用的验证入口。它是变量而非函数,因为
// 入站握手的验证结果是**集成测试唯一无法从外部构造的输入**(凭据由 server 签),
// 测试经 SwapAccountCredentialVerifierForTest 换掉它。
var verifyInboundAccountCredential = newAccountCredentialVerifier(serverAccountState).Verify

// SwapAccountCredentialVerifierForTest 换掉入站账号凭据验证器并交回还原函数。
// 与 agentruntime.SwapRuntimeForTest 同一模式:只给测试用。
func SwapAccountCredentialVerifierForTest(verify func(context.Context, string) (string, error)) func() {
	previous := verifyInboundAccountCredential
	verifyInboundAccountCredential = verify
	return func() { verifyInboundAccountCredential = previous }
}
