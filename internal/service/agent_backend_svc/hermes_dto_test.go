package agent_backend_svc

import (
	"context"
	"testing"

	"github.com/cago-frame/cago/pkg/consts"
	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/agentre-hub/agentre/internal/model/entity/agent_backend_entity"
)

// hermes 的 Server URL 存在 entity 的 config_json 上，只有 service 层显式读写才会
// 进出 DTO。漏了就静默：请求 JSON 里的未知键会被丢掉、item 也不回传，前端表现为
// 「保存后重开是空的」。
// hermes 的 gated 展示字段与 Server URL 一样：只有 service 层显式读写才会进出
// DTO，漏一格就表现为「登录成功、保存后重开又是未登录」。
func TestBackend_GivenHermesAuthDisplayFields_ThenTheyRoundTripThroughDTO(t *testing.T) {
	convey.Convey("Given a hermes backend carrying auth display fields", t, func() {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)

		convey.Convey("创建时写进 entity，并随 item 回到前端", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "h-auth").Return(nil, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "basic", b.HermesAuthProvider)
					assert.Equal(t, "user-7", b.HermesUserID)
					b.ID = 81
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:               string(agent_backend_entity.TypeHermes),
				Name:               "h-auth",
				HermesURL:          "http://10.0.0.8:9119",
				HermesAuthProvider: "basic",
				HermesUserID:       "user-7",
			})
			require.NoError(t, err)
			assert.Equal(t, "basic", resp.Item.HermesAuthProvider)
			assert.Equal(t, "user-7", resp.Item.HermesUserID)
		})

		convey.Convey("更新时同样落库并回传", func() {
			existing := &agent_backend_entity.AgentBackend{
				ID: 82, Type: string(agent_backend_entity.TypeHermes), Name: "h-auth",
				HermesURL: "http://10.0.0.8:9119", HermesAuthProvider: "basic", HermesUserID: "user-7",
				Status: consts.ACTIVE,
			}
			backendMock.EXPECT().Find(gomock.Any(), int64(82)).Return(existing, nil)
			backendMock.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "basic", b.HermesAuthProvider)
					assert.Equal(t, "user-8", b.HermesUserID)
					return nil
				})

			resp, err := svc.Update(ctx, &UpdateBackendRequest{
				ID: 82, Name: "h-auth",
				HermesURL:          "http://10.0.0.8:9119",
				HermesAuthProvider: "basic",
				HermesUserID:       "user-8",
			})
			require.NoError(t, err)
			assert.Equal(t, "basic", resp.Item.HermesAuthProvider)
			assert.Equal(t, "user-8", resp.Item.HermesUserID)
		})
	})
}

func TestBackend_GivenHermesURL_ThenItRoundTripsThroughDTO(t *testing.T) {
	convey.Convey("Given a hermes backend carrying a Server URL", t, func() {
		ctx, backendMock, _, _, _, svc := setupSvcTest(t)

		convey.Convey("创建时写进 entity，并随 item 回到前端", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "h1").Return(nil, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, string(agent_backend_entity.TypeHermes), b.Type)
					assert.Equal(t, "http://127.0.0.1:9119", b.HermesURL)
					b.ID = 77
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:      string(agent_backend_entity.TypeHermes),
				Name:      "h1",
				HermesURL: "http://127.0.0.1:9119",
			})
			require.NoError(t, err)
			assert.Equal(t, "http://127.0.0.1:9119", resp.Item.HermesURL)
		})

		convey.Convey("ws:// 会在落库前归一成 http://", func() {
			backendMock.EXPECT().FindByName(gomock.Any(), "h1").Return(nil, nil)
			backendMock.EXPECT().Create(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "http://127.0.0.1:9119", b.HermesURL)
					b.ID = 78
					return nil
				})

			resp, err := svc.Create(ctx, &CreateBackendRequest{
				Type:      string(agent_backend_entity.TypeHermes),
				Name:      "h1",
				HermesURL: "ws://127.0.0.1:9119",
			})
			require.NoError(t, err)
			assert.Equal(t, "http://127.0.0.1:9119", resp.Item.HermesURL)
		})

		convey.Convey("更新时同样落库", func() {
			existing := &agent_backend_entity.AgentBackend{
				ID: 79, Type: string(agent_backend_entity.TypeHermes), Name: "h2", Status: consts.ACTIVE,
			}
			backendMock.EXPECT().Find(gomock.Any(), int64(79)).Return(existing, nil)
			backendMock.EXPECT().Update(gomock.Any(), gomock.AssignableToTypeOf(&agent_backend_entity.AgentBackend{})).
				DoAndReturn(func(_ context.Context, b *agent_backend_entity.AgentBackend) error {
					assert.Equal(t, "https://hermes.example.com:443", b.HermesURL)
					return nil
				})

			_, err := svc.Update(ctx, &UpdateBackendRequest{
				ID: 79, Name: "h2", HermesURL: "https://hermes.example.com:443",
			})
			require.NoError(t, err)
		})
	})
}
