package state

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestState_JSONRoundTrip(t *testing.T) {
	in := &State{
		SchemaVersion:      1,
		DaemonInstanceUUID: "8f3a9c2b",
		HubServerURL:       "https://hub.example",
		Listen: ListenPrefs{
			LanHost:     "0.0.0.0",
			LanPort:     7456,
			TLSCertFile: "/etc/ssl/cert.pem",
			TLSKeyFile:  "/etc/ssl/key.pem",
		},
		PairedPeers: map[string]PairedPeer{
			"sha256:abc": {
				DeviceName:  "mac-pro-m4",
				DeviceToken: "tok123",
				PairedAt:    1716000000,
				LastSeenAt:  1716001000,
			},
		},
		LLMProviders: map[string]LLMProviderMeta{
			"42": { //nolint:gosec // credential-shaped API key is a test fixture.
				Name: "anthropic-main", Type: "anthropic",
				BaseURL:     "https://api.anthropic.com",
				APIKey:      "sk-ant-real-key",
				Model:       "claude-sonnet-4-6",
				ModelRoutes: map[string]string{"OPUS": "claude-opus-4"},
				UpdatedAt:   1716000500,
			},
		},
		Preferences: Preferences{
			LogLevel:              "info",
			LogRotateMB:           50,
			PairingCodeTTLSeconds: 300,
			PairingRateLimit: RateLimitPrefs{
				MaxAttemptsPerIP: 3,
				WindowSeconds:    60,
			},
		},
	}

	b, err := json.Marshal(in)
	require.NoError(t, err)

	var out State
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in, &out)
}

func TestState_AccountLoginRoundTripStoresOnlyOpaqueAccountData(t *testing.T) {
	credentialType := reflect.TypeFor[AccountCredential]()
	credentialFields := make([]string, credentialType.NumField())
	for i := 0; i < credentialType.NumField(); i++ {
		credentialFields[i] = credentialType.Field(i).Name
	}
	assert.Equal(t, []string{
		"DeviceID", "AccessToken", "AccessTokenExpiresAt", "RefreshToken", "RefreshTokenExpiresAt",
	}, credentialFields, "account credentials must not acquire PII fields")

	in := &State{
		SchemaVersion:            1,
		DaemonInstanceUUID:       "daemon-uuid",
		AccountID:                "account-42",
		VerificationPublicKeyPEM: "-----BEGIN PUBLIC KEY-----\\nkey",
		Credential: AccountCredential{
			DeviceID:              9,
			AccessToken:           "access-token",
			AccessTokenExpiresAt:  1716003600,
			RefreshToken:          "refresh-token",
			RefreshTokenExpiresAt: 1723776000,
		},
	}

	b, err := json.Marshal(in)
	require.NoError(t, err)
	assert.NotContains(t, string(b), "email")
	assert.NotContains(t, string(b), "avatar")
	assert.NotContains(t, string(b), "displayName")

	var out State
	require.NoError(t, json.Unmarshal(b, &out))
	assert.Equal(t, in.AccountID, out.AccountID)
	assert.Equal(t, in.VerificationPublicKeyPEM, out.VerificationPublicKeyPEM)
	assert.Equal(t, in.Credential, out.Credential)
}

// TestState_LLMModelCatalogRoundTrip 钉死 task 6 多模型目录的持久化：Provider 名下
// 保存 DefaultModelKey + Models（稳定 key + 实际 model id + 启用态），APIKey 仍只存
// Provider 级、不进模型行。
func TestState_LLMModelCatalogRoundTrip(t *testing.T) {
	in := &State{
		SchemaVersion:      1,
		DaemonInstanceUUID: "daemon-uuid",
		LLMProviders: map[string]LLMProviderMeta{
			"prov-1": { //nolint:gosec // credential-shaped API key is a test fixture.
				Name:            "Anthropic Prod",
				Type:            "anthropic",
				APIKey:          "sk-ant-secret",
				Model:           "claude-sonnet-4-6",
				DefaultModelKey: "model-1",
				Models: []LLMModelMeta{
					{ModelKey: "model-1", ModelID: "claude-sonnet-4-6", Name: "Sonnet", Enabled: true},
					{ModelKey: "model-2", ModelID: "claude-opus-4-5", Enabled: true},
					{ModelKey: "model-3", ModelID: "claude-haiku-gone", Enabled: false},
				},
				UpdatedAt: 1716000500,
			},
		},
	}
	b, err := json.Marshal(in)
	require.NoError(t, err)

	var out State
	require.NoError(t, json.Unmarshal(b, &out))
	require.Equal(t, in, &out)
	assert.Len(t, out.LLMProviders["prov-1"].Models, 3)
	assert.Equal(t, "model-1", out.LLMProviders["prov-1"].DefaultModelKey)
}

func TestState_DefaultsAreSane(t *testing.T) {
	s := NewDefault("uuid-x")
	assert.Equal(t, 1, s.SchemaVersion)
	assert.Equal(t, "uuid-x", s.DaemonInstanceUUID)
	assert.Equal(t, "0.0.0.0", s.Listen.LanHost)
	assert.Equal(t, 7456, s.Listen.LanPort)
	assert.Empty(t, s.HubServerURL)
	assert.Equal(t, "info", s.Preferences.LogLevel)
	assert.Equal(t, 300, s.Preferences.PairingCodeTTLSeconds)
	assert.Equal(t, 3, s.Preferences.PairingRateLimit.MaxAttemptsPerIP)
	assert.NotNil(t, s.PairedPeers)
	assert.NotNil(t, s.LLMProviders)
}
