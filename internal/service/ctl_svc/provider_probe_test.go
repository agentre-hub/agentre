package ctl_svc

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/pkg/agenttool"
	"github.com/agentre-hub/agentre/internal/service/ctl_svc/mock_ctl_svc"
	"github.com/agentre-hub/agentre/internal/service/llm_provider_svc"
)

type fakeProbes struct {
	testReq  [2]any
	test     ProbeResult
	models   []DiscoveredModel
	probeErr error
}

func (f *fakeProbes) TestProvider(_ context.Context, providerID int64, modelKey string) (ProbeResult, error) {
	f.testReq = [2]any{providerID, modelKey}
	return f.test, f.probeErr
}

func (f *fakeProbes) DiscoverModels(_ context.Context, providerID int64) ([]DiscoveredModel, error) {
	f.testReq = [2]any{providerID, ""}
	return f.models, f.probeErr
}

func probeHandler(p ProviderProbes) *ctlHandler {
	res := fakeResources()
	res.Probes = p
	return &ctlHandler{token: testToken, sessions: agenttool.NewTokenSigner(), resources: res}
}

func TestProviderTest_GivenASavedProvider_ThenReportsTheServiceResultAndPassesTheModelKey(t *testing.T) {
	f := &fakeProbes{test: ProbeResult{OK: true, Message: "ok"}}
	rec := do(t, probeHandler(f), http.MethodPost, "/ctl/v1/provider-test", testToken, `{"providerId":4,"modelKey":"k1"}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"ok":true,"message":"ok"}`, rec.Body.String())
	assert.Equal(t, [2]any{int64(4), "k1"}, f.testReq)
}

func TestProviderTest_GivenAnUpstreamFailure_ThenItIsAnOKFalseResultNotAnHTTPError(t *testing.T) {
	f := &fakeProbes{test: ProbeResult{OK: false, Message: "401 unauthorized"}}
	rec := do(t, probeHandler(f), http.MethodPost, "/ctl/v1/provider-test", testToken, `{"providerId":4}`)
	require.Equal(t, http.StatusOK, rec.Code)
	assert.JSONEq(t, `{"ok":false,"message":"401 unauthorized"}`, rec.Body.String())
}

func TestProviderTest_GivenBadRequests_ThenTheyAreRefused(t *testing.T) {
	h := probeHandler(&fakeProbes{})
	assert.Equal(t, http.StatusMethodNotAllowed, do(t, h, http.MethodGet, "/ctl/v1/provider-test", testToken, "").Code)
	assert.Equal(t, http.StatusBadRequest, do(t, h, http.MethodPost, "/ctl/v1/provider-test", testToken, `{}`).Code, "providerId is required")
	assert.Equal(t, http.StatusServiceUnavailable, do(t, probeHandler(nil), http.MethodPost, "/ctl/v1/provider-test", testToken, `{"providerId":4}`).Code)
	rec := do(t, probeHandler(&fakeProbes{probeErr: errors.New("boom")}), http.MethodPost, "/ctl/v1/provider-test", testToken, `{"providerId":4}`)
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Body.String(), "boom")
}

func TestProviderDiscover_ReturnsTheUpstreamModels(t *testing.T) {
	f := &fakeProbes{models: []DiscoveredModel{{ModelID: "gpt-x", Name: "GPT X"}}}
	rec := do(t, probeHandler(f), http.MethodPost, "/ctl/v1/provider-discover", testToken, `{"providerId":4}`)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `{"models":[{"modelId":"gpt-x","name":"GPT X"}]}`, rec.Body.String())
	assert.Equal(t, http.StatusBadRequest, do(t, probeHandler(f), http.MethodPost, "/ctl/v1/provider-discover", testToken, `{}`).Code)
}

func TestProviderProbes_AreReadsSoSessionTokensMayCallThem(t *testing.T) {
	signer := agenttool.NewTokenSigner()
	res := fakeResources()
	res.Probes = &fakeProbes{}
	h := &ctlHandler{token: testToken, sessions: signer, resources: res}
	tok := signer.MintToken(7, 42)
	for _, route := range []string{"provider-test", "provider-discover"} {
		rec := do(t, h, http.MethodPost, "/ctl/v1/"+route, tok, `{"providerId":4}`)
		assert.Equal(t, http.StatusOK, rec.Code, route)
	}
}

func TestLiveProbes_MapTheProviderService(t *testing.T) {
	ctrl := gomock.NewController(t)
	svc := mock_ctl_svc.NewMockProviderService(ctrl)
	p := liveProbes{providers: func() ProviderService { return svc }}

	svc.EXPECT().TestConnection(gomock.Any(), &llm_provider_svc.TestConnectionRequest{ID: 4, ModelKey: "k1"}).
		Return(&llm_provider_svc.TestConnectionResponse{OK: false, Message: "bad key"}, nil)
	got, err := p.TestProvider(context.Background(), 4, "k1")
	require.NoError(t, err)
	assert.Equal(t, ProbeResult{OK: false, Message: "bad key"}, got)

	svc.EXPECT().PreviewModels(gomock.Any(), &llm_provider_svc.PreviewModelsRequest{ID: 4}).
		Return(&llm_provider_svc.PreviewModelsResponse{Items: []*llm_provider_svc.ModelInfo{{ID: "m1"}, nil, {ID: "m2"}}}, nil)
	models, err := p.DiscoverModels(context.Background(), 4)
	require.NoError(t, err)
	assert.Equal(t, []DiscoveredModel{{ModelID: "m1", Name: "m1"}, {ModelID: "m2", Name: "m2"}}, models)

	svc.EXPECT().PreviewModels(gomock.Any(), gomock.Any()).Return(nil, errors.New("upstream down"))
	_, err = p.DiscoverModels(context.Background(), 4)
	assert.EqualError(t, err, "upstream down")
}
