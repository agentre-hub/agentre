// Package state owns agentred's single persistent JSON file.
// It is the only place that writes state.json; all callers go through Load/Mutate.
package state

import "sync"

// State is the on-disk shape persisted to <AppDataDir>/state.json.
type State struct {
	SchemaVersion      int                   `json:"schemaVersion"`
	DaemonInstanceUUID string                `json:"daemonInstanceUUID"`
	AccountServerURL   string                `json:"accountServerURL,omitempty"`
	Listen             ListenPrefs           `json:"listen"`
	PairedPeers        map[string]PairedPeer `json:"pairedPeers"`
	// LLMProviders 是从账号引擎快照拉下来的供应商**元数据**,每次快照整份替换。它没有
	// key 这一格:账号供应商的 key 只经租约在内存里拿(见 internal/daemon/leases)。
	LLMProviders map[string]LLMProviderMeta `json:"llmProviders"`
	// LocalLLMProviders 是在这台机器上用 `agentred llm add` 添加的供应商,带着自己的 key。
	// 那是用户在这台机器上的明确操作:快照拉取不覆盖它,logout 也不清它。
	LocalLLMProviders map[string]LocalLLMProvider `json:"localLlmProviders,omitempty"`
	Preferences       Preferences                 `json:"preferences"`
	AccountID         string                      `json:"accountId,omitempty"`
	Credential        AccountCredential           `json:"credential,omitempty"`
	// DirectCredentials are the local direct credentials this daemon issued,
	// keyed by the desktop fingerprint the account server named at issuance.
	// Account-bound: Logout drops them with the claim (D11).
	DirectCredentials map[string]DirectCredential `json:"directCredentials,omitempty"`
	// BackendCredentials are the device-local secrets of the network agent
	// backends bound to this daemon (OpenClaw Gateway tokens keyed by sync_id,
	// Hermes refresh tokens keyed by serve URL, the OpenClaw identity seed),
	// keyed by the backendcred slot name. They belong to the account's
	// backends, so Logout drops them with the claim.
	BackendCredentials map[string]string `json:"backendCredentials,omitempty"`
	// HermesIdentities are the non-sensitive display identities of the Hermes
	// serves this daemon is logged into, keyed by normalized serve URL.
	HermesIdentities map[string]HermesIdentity `json:"hermesIdentities,omitempty"`

	mu  *sync.RWMutex `json:"-"`
	dir string        `json:"-"`
}

type ListenPrefs struct {
	LanHost string `json:"lanHost"`
	LanPort int    `json:"lanPort"`
	// TLS keeps the LAN port wss-only. Without TLSCertFile/TLSKeyFile the
	// daemon serves the certificate it generates in its data directory.
	TLS         bool   `json:"tls,omitempty"`
	TLSCertFile string `json:"tlsCertFile,omitempty"`
	TLSKeyFile  string `json:"tlsKeyFile,omitempty"`
	// AdvertiseAddr is the address other machines reach this daemon at —
	// pairing and automatic direct alike — instead of the ones it sees on its
	// own interfaces, for a daemon behind NAT that can derive neither the
	// reachable host nor the mapped port from the inside.
	AdvertiseAddr string `json:"advertiseAddr,omitempty"`
}

type PairedPeer struct {
	DeviceName  string `json:"deviceName"`
	DeviceToken string `json:"deviceToken"`
	PairedAt    int64  `json:"pairedAt"`
	LastSeenAt  int64  `json:"lastSeenAt"`
}

// LLMModelMeta 是 daemon 侧保存的 Provider 名下一条模型的非敏感元数据（task 6 多模型）。
// ModelKey 稳定不可变（跨实体引用），ModelID 是执行时发给上游的实际模型 id。
// Enabled=false 表示该模型不可用于执行。
type LLMModelMeta struct {
	ModelKey      string `json:"modelKey"`
	ModelID       string `json:"modelId"`
	Name          string `json:"name,omitempty"`
	Enabled       bool   `json:"enabled,omitempty"`
	ContextWindow *int64 `json:"contextWindow,omitempty"`
	MaxOutput     *int64 `json:"maxOutput,omitempty"`
}

// LLMProviderMeta 是一个供应商的非敏感元数据。它**没有** key 字段 —— 结构上就写不进
// state.json(规格「agentred：按需租用」)。
type LLMProviderMeta struct {
	Name        string            `json:"name"`
	Type        string            `json:"type"`
	BaseURL     string            `json:"baseURL"`
	Model       string            `json:"model"`
	ModelRoutes map[string]string `json:"modelRoutes"`
	// DefaultModelKey 是该 Provider 当前默认模型的稳定 key（task 6 多模型）。
	// 空 = 无默认模型。provider-default 解析要求默认模型存在且启用，否则按配置损坏
	// 严格阻止（见 ProviderLookup.ResolveModel），不回落旧单模型字段。
	DefaultModelKey string         `json:"defaultModelKey,omitempty"`
	Models          []LLMModelMeta `json:"models,omitempty"`
	UpdatedAt       int64          `json:"updatedAt"`
}

// LocalLLMProvider 是本机添加的供应商:元数据加上它自己的 key。
type LocalLLMProvider struct {
	LLMProviderMeta
	APIKey string `json:"apiKey"`
}

// ProviderEntry 是查一个供应商的结果。Local 为 true 时 APIKey 是本机添加时给的 key;
// 账号供应商(Local 为 false)只有元数据,APIKey 恒空,key 要另去租。
type ProviderEntry struct {
	LLMProviderMeta
	APIKey string
	Local  bool
}

// AccountCredential is the daemon's refreshable account credential. It
// deliberately contains no user profile data: AccountID is the only account
// identity retained by agentred.
type AccountCredential struct {
	DeviceID              int64  `json:"deviceId,omitempty"`
	AccessToken           string `json:"accessToken,omitempty"`
	AccessTokenExpiresAt  int64  `json:"accessTokenExpiresAt,omitempty"`
	RefreshToken          string `json:"refreshToken,omitempty"`
	RefreshTokenExpiresAt int64  `json:"refreshTokenExpiresAt,omitempty"`
}

// DirectCredential is one desktop's local direct credential: the opaque value
// the desktop presents on auth.direct, and the account the daemon belonged to
// when it issued it.
type DirectCredential struct {
	Credential string `json:"credential"`
	AccountID  string `json:"accountId"`
}

// HermesIdentity is what a Hermes login reports about who logged in. It is
// shown to the user and carries no secret.
type HermesIdentity struct {
	Provider string `json:"provider"`
	UserID   string `json:"userId"`
}

type Preferences struct {
	LogLevel              string         `json:"logLevel"`
	LogRotateMB           int            `json:"logRotateMB"`
	PairingCodeTTLSeconds int            `json:"pairingCodeTTLSeconds"`
	PairingRateLimit      RateLimitPrefs `json:"pairingRateLimit"`
}

type RateLimitPrefs struct {
	MaxAttemptsPerIP int `json:"maxAttemptsPerIP"`
	WindowSeconds    int `json:"windowSeconds"`
}

// NewDefault builds a State with safe defaults; caller provides a stable UUID.
func NewDefault(daemonUUID string) *State {
	return &State{
		SchemaVersion:      1,
		DaemonInstanceUUID: daemonUUID,
		Listen:             ListenPrefs{LanHost: "0.0.0.0", LanPort: 7456},
		PairedPeers:        map[string]PairedPeer{},
		LLMProviders:       map[string]LLMProviderMeta{},
		Preferences: Preferences{
			LogLevel:              "info",
			LogRotateMB:           50,
			PairingCodeTTLSeconds: 300,
			PairingRateLimit: RateLimitPrefs{
				MaxAttemptsPerIP: 3,
				WindowSeconds:    60,
			},
		},
		mu: &sync.RWMutex{},
	}
}
