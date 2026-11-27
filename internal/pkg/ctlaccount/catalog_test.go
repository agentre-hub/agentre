package ctlaccount

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/agentre-hub/agentre/internal/pkg/ctlclient"
	"github.com/agentre-hub/agentre/pkg/wire/agentrewire"
)

// fixedCatalog 是一个按类型给固定列表的资源执行者。
func fixedCatalog(t *testing.T, items map[agentrewire.CtlKind][]*agentrewire.CtlResource) *ResourceCatalog {
	t.Helper()
	return NewResourceCatalog(func(_ context.Context, body []byte) ([]byte, error) {
		var req agentrewire.CtlRequest
		require.NoError(t, protojson.Unmarshal(body, &req))
		return protojson.Marshal(&agentrewire.CtlResponse{Result: &agentrewire.CtlResponse_List{
			List: &agentrewire.CtlListResponse{Items: items[req.GetList().GetKind()]}}})
	})
}

func project(id, parent int64, name string) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Project{Project: &agentrewire.CtlProject{Id: id, ParentId: parent, Name: name}}}
}

func agent(id int64, name string) *agentrewire.CtlResource {
	return &agentrewire.CtlResource{Doc: &agentrewire.CtlResource_Agent{Agent: &agentrewire.CtlAgent{Id: id, Name: name}}}
}

func TestResourceCatalog_GivenRefs_WhenLocated_ThenIdNamePathAndModelResolveAndAmbiguityIsTyped(t *testing.T) {
	c := fixedCatalog(t, map[agentrewire.CtlKind][]*agentrewire.CtlResource{
		agentrewire.CtlKind_CTL_KIND_AGENT:    {agent(4, "reviewer"), agent(5, "2024")},
		agentrewire.CtlKind_CTL_KIND_PROJECT:  {project(1, 0, "agentre"), project(2, 1, "docs"), project(3, 0, "docs")},
		agentrewire.CtlKind_CTL_KIND_PROVIDER: {{Doc: &agentrewire.CtlResource_Provider{Provider: &agentrewire.CtlProvider{Id: 7, Name: "openrouter"}}}},
		agentrewire.CtlKind_CTL_KIND_MODEL: {{Doc: &agentrewire.CtlResource_Model{Model: &agentrewire.CtlModel{
			Id: 8, ProviderId: 7, ModelId: "z-ai/glm-5"}}}},
	})

	it, err := c.Locate(agentrewire.CtlKind_CTL_KIND_AGENT, "4")
	require.NoError(t, err)
	assert.Equal(t, "reviewer", it.GetAgent().GetName(), "a numeric ref is an id first")
	it, err = c.Locate(agentrewire.CtlKind_CTL_KIND_AGENT, "2024")
	require.NoError(t, err)
	assert.Equal(t, int64(5), it.GetAgent().GetId(), "an id that misses falls back to the name")
	it, err = c.Locate(agentrewire.CtlKind_CTL_KIND_PROJECT, "agentre/docs")
	require.NoError(t, err)
	assert.Equal(t, int64(2), it.GetProject().GetId())
	assert.Equal(t, "agentre/docs", c.Path(agentrewire.CtlKind_CTL_KIND_PROJECT, 2))
	it, err = c.Locate(agentrewire.CtlKind_CTL_KIND_MODEL, "openrouter/z-ai/glm-5")
	require.NoError(t, err)
	assert.Equal(t, int64(8), it.GetModel().GetId(), "a model id may itself contain a slash")

	_, err = c.Locate(agentrewire.CtlKind_CTL_KIND_PROJECT, "docs")
	var ambiguous *AmbiguousError
	require.True(t, errors.As(err, &ambiguous), "two projects end in docs: %v", err)
	var usage *UsageError
	assert.True(t, errors.As(err, &usage), "ambiguity is the caller's to fix")

	_, err = c.Locate(agentrewire.CtlKind_CTL_KIND_AGENT, "ghost")
	assert.EqualError(t, err, `agent "ghost" not found`)
	assert.False(t, errors.As(err, &usage))
}

func TestResourceCatalog_GivenTheExecutorRefuses_ThenItsMessageIsShownAsIs(t *testing.T) {
	c := NewResourceCatalog(func(context.Context, []byte) ([]byte, error) {
		return nil, &ctlclient.ServerError{Status: 403, Message: "this token lacks the read scope"}
	})

	_, err := c.List(agentrewire.CtlKind_CTL_KIND_AGENT)

	assert.EqualError(t, err, "this token lacks the read scope")
}
