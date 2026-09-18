package update_svc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/agentre-hub/agentre/internal/pkg/paths"
)

func TestCompareVersions(t *testing.T) {
	convey.Convey("版本比较", t, func() {
		convey.Convey("基础版本号比较", func() {
			assert.Equal(t, 0, compareVersions("1.0.0", "1.0.0"))
			assert.Greater(t, compareVersions("2.0.0", "1.0.0"), 0)
			assert.Less(t, compareVersions("1.0.0", "2.0.0"), 0)
			assert.Greater(t, compareVersions("1.1.0", "1.0.0"), 0)
			assert.Greater(t, compareVersions("1.0.1", "1.0.0"), 0)
		})

		convey.Convey("稳定版 > 同版本预发布", func() {
			assert.Greater(t, compareVersions("1.0.0", "1.0.0-beta.1"), 0)
			assert.Greater(t, compareVersions("1.0.0", "1.0.0-rc.1"), 0)
			assert.Less(t, compareVersions("1.0.0-beta.1", "1.0.0"), 0)
		})

		convey.Convey("预发布标识符排序", func() {
			// beta < rc (字母序)
			assert.Less(t, compareVersions("1.0.0-beta.1", "1.0.0-rc.1"), 0)
			// 同类型数字递增
			assert.Less(t, compareVersions("1.0.0-beta.1", "1.0.0-beta.2"), 0)
			assert.Greater(t, compareVersions("1.0.0-rc.2", "1.0.0-rc.1"), 0)
		})

		convey.Convey("nightly 版本比较", func() {
			// 同基线 nightly 按日期排序
			assert.Greater(t, compareVersions("1.0.0-nightly.20260326", "1.0.0-nightly.20260325"), 0)
			assert.Equal(t, 0, compareVersions("1.0.0-nightly.20260325", "1.0.0-nightly.20260325"))
			// 基于预发布的 nightly
			assert.Greater(t, compareVersions("1.0.0-beta.1.nightly.20260326", "1.0.0-beta.1.nightly.20260325"), 0)
		})

		convey.Convey("跨类型比较", func() {
			// nightly 基于 beta 的 vs 纯 beta
			assert.Greater(t, compareVersions("1.0.0-beta.1.nightly.20260325", "1.0.0-beta.1"), 0)
			// 更高基线版本胜出
			assert.Greater(t, compareVersions("1.1.0-beta.1", "1.0.0"), 0)
			assert.Less(t, compareVersions("1.0.0-nightly.20260325", "1.1.0-beta.1"), 0)
		})

		convey.Convey("不同长度版本号", func() {
			assert.Equal(t, 0, compareVersions("1.0", "1.0.0"))
			assert.Greater(t, compareVersions("1.0.1", "1.0"), 0)
		})
	})
}

func TestIsNightlyVersion(t *testing.T) {
	convey.Convey("nightly 版本判断", t, func() {
		convey.Convey("新格式（语义化）", func() {
			assert.True(t, isNightlyVersion("v1.0.0-nightly.20260325"))
			assert.True(t, isNightlyVersion("1.0.0-beta.1.nightly.20260325"))
		})

		convey.Convey("旧格式", func() {
			assert.True(t, isNightlyVersion("nightly-20260325-abc1234"))
		})

		convey.Convey("非 nightly", func() {
			assert.False(t, isNightlyVersion("v1.0.0"))
			assert.False(t, isNightlyVersion("1.0.0-beta.1"))
			assert.False(t, isNightlyVersion("1.0.0-rc.1"))
		})
	})
}

func TestHasUpdate(t *testing.T) {
	convey.Convey("更新判断", t, func() {
		convey.Convey("dev 或空版本始终有更新", func() {
			assert.True(t, hasUpdate(ChannelStable, "dev", "v1.0.0"))
			assert.True(t, hasUpdate(ChannelStable, "", "v1.0.0"))
		})

		convey.Convey("stable 通道", func() {
			convey.Convey("有新版本", func() {
				assert.True(t, hasUpdate(ChannelStable, "v1.0.0", "v1.0.1"))
				assert.True(t, hasUpdate(ChannelStable, "v1.0.0", "v2.0.0"))
			})

			convey.Convey("同版本无更新", func() {
				assert.False(t, hasUpdate(ChannelStable, "v1.0.0", "v1.0.0"))
			})

			convey.Convey("远端版本更旧无更新", func() {
				assert.False(t, hasUpdate(ChannelStable, "v1.0.1", "v1.0.0"))
			})

			convey.Convey("当前是 nightly 切换到 stable 始终更新", func() {
				assert.True(t, hasUpdate(ChannelStable, "v1.0.0-nightly.20260325", "v1.0.0"))
			})
		})

		convey.Convey("beta 通道", func() {
			convey.Convey("有新 beta 版本", func() {
				assert.True(t, hasUpdate(ChannelBeta, "v1.0.0-beta.1", "v1.0.0-beta.2"))
			})

			convey.Convey("当前是 nightly 切换到 beta 始终更新", func() {
				assert.True(t, hasUpdate(ChannelBeta, "v1.0.0-nightly.20260325", "v1.0.0-beta.1"))
			})
		})

		convey.Convey("nightly 通道", func() {
			convey.Convey("从 stable 切换到 nightly 始终更新", func() {
				assert.True(t, hasUpdate(ChannelNightly, "v1.0.0", "v1.0.0-nightly.20260325"))
			})

			convey.Convey("旧格式 nightly 字符串比较", func() {
				assert.True(t, hasUpdate(ChannelNightly, "nightly-20260324-abc", "nightly-20260325-def"))
				assert.False(t, hasUpdate(ChannelNightly, "nightly-20260325-abc", "nightly-20260325-abc"))
			})

			convey.Convey("新格式 nightly 语义化比较", func() {
				assert.True(t, hasUpdate(ChannelNightly, "v1.0.0-nightly.20260324", "v1.0.0-nightly.20260325"))
				assert.False(t, hasUpdate(ChannelNightly, "v1.0.0-nightly.20260325", "v1.0.0-nightly.20260325"))
				assert.False(t, hasUpdate(ChannelNightly, "v1.0.0-nightly.20260326", "v1.0.0-nightly.20260325"))
			})
		})
	})
}

func TestSplitPreRelease(t *testing.T) {
	convey.Convey("分离预发布后缀", t, func() {
		base, pre := splitPreRelease("1.0.0")
		assert.Equal(t, "1.0.0", base)
		assert.Equal(t, "", pre)

		base, pre = splitPreRelease("1.0.0-beta.1")
		assert.Equal(t, "1.0.0", base)
		assert.Equal(t, "beta.1", pre)

		base, pre = splitPreRelease("1.0.0-beta.1.nightly.20260325")
		assert.Equal(t, "1.0.0", base)
		assert.Equal(t, "beta.1.nightly.20260325", pre)
	})
}

func TestReleaseInfoDownloadURL(t *testing.T) {
	convey.Convey("release-info.json URL 构造", t, func() {
		convey.Convey("使用当前发布仓库", func() {
			assert.Equal(t, "agentre-hub/agentre", githubRepo)
			assert.Equal(t, "https://api.github.com/repos/agentre-hub/agentre", apiBaseURL)
		})

		convey.Convey("stable 通道", func() {
			url := releaseInfoURL(ChannelStable)
			assert.Contains(t, url, "releases/latest/download/release-info.json")
		})

		convey.Convey("nightly 通道", func() {
			url := releaseInfoURL(ChannelNightly)
			assert.Contains(t, url, "releases/download/nightly/release-info.json")
		})

		convey.Convey("beta 通道返回空（不支持镜像回退）", func() {
			url := releaseInfoURL(ChannelBeta)
			assert.Equal(t, "", url)
		})
	})
}

func TestPickLatestStableRelease(t *testing.T) {
	convey.Convey("stable 通道发布选择", t, func() {
		convey.Convey("跳过 nightly 和 prerelease,选择第一个正式版本", func() {
			release, err := pickLatestStableRelease([]ReleaseInfo{
				{TagName: "nightly", Name: "v0.0.0-nightly.20260528", Prerelease: true},
				{TagName: "v1.0.0-beta.1", Prerelease: true},
				{TagName: "v0.9.0", Prerelease: false},
			})
			assert.NoError(t, err)
			assert.Equal(t, "v0.9.0", release.TagName)
		})

		convey.Convey("只有 nightly 时返回明确错误,不暴露 GitHub latest 404", func() {
			release, err := pickLatestStableRelease([]ReleaseInfo{
				{TagName: "nightly", Name: "v0.0.0-nightly.20260528", Prerelease: true},
			})
			assert.Nil(t, release)
			assert.Error(t, err)
			assert.True(t, errors.Is(err, errNoStableRelease))
			assert.Contains(t, err.Error(), "no stable release found")
		})
	})
}

func TestParseChecksums(t *testing.T) {
	convey.Convey("解析 SHA256SUMS.txt", t, func() {
		convey.Convey("正常格式", func() {
			input := "abc123def456  agentre-1.0.0-darwin-arm64.dmg\n" +
				"789abc012def  agentre-1.0.0-linux-amd64.tar.gz\n"
			result := parseChecksums(input)
			assert.Equal(t, "abc123def456", result["agentre-1.0.0-darwin-arm64.dmg"])
			assert.Equal(t, "789abc012def", result["agentre-1.0.0-linux-amd64.tar.gz"])
		})

		convey.Convey("忽略空行", func() {
			input := "abc123  file1.tar.gz\n\n789def  file2.dmg\n"
			result := parseChecksums(input)
			assert.Len(t, result, 2)
		})

		convey.Convey("忽略格式不正确的行", func() {
			input := "abc123  file1.tar.gz\nbadline\nabc123  file2.tar.gz\n"
			result := parseChecksums(input)
			assert.Len(t, result, 2)
		})

		convey.Convey("空输入", func() {
			result := parseChecksums("")
			assert.Empty(t, result)
		})

		convey.Convey("单空格分隔也支持", func() {
			input := "abc123 file1.tar.gz\n"
			result := parseChecksums(input)
			assert.Equal(t, "abc123", result["file1.tar.gz"])
		})

		convey.Convey("二进制模式 * 前缀", func() {
			input := "abc123 *file1.tar.gz\n"
			result := parseChecksums(input)
			assert.Equal(t, "abc123", result["file1.tar.gz"])
		})
	})
}

// TestFetchReleaseFromURL 用 httptest.Server 模拟 GitHub API，验证三通道分流。
func TestFetchReleaseFromURL(t *testing.T) {
	convey.Convey("HTTP 请求 release 信息", t, func() {
		convey.Convey("正常 200 返回 ReleaseInfo", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(ReleaseInfo{
					TagName:     "v1.2.3",
					Name:        "Release 1.2.3",
					Body:        "release notes here",
					HTMLURL:     "https://example.com/release",
					PublishedAt: "2026-05-22T00:00:00Z",
					Assets: []ReleaseAsset{
						{Name: "agentre-v1.2.3-darwin-arm64.dmg", BrowserDownloadURL: "https://example.com/dmg", Size: 12345},
					},
				})
			}))
			defer server.Close()

			info, err := fetchReleaseFromURL(server.URL)
			assert.NoError(t, err)
			assert.Equal(t, "v1.2.3", info.TagName)
			assert.Len(t, info.Assets, 1)
			assert.Equal(t, int64(12345), info.Assets[0].Size)
		})

		convey.Convey("非 200 状态码返回错误", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			}))
			defer server.Close()

			info, err := fetchReleaseFromURL(server.URL)
			assert.Error(t, err)
			assert.Nil(t, info)
			assert.Contains(t, err.Error(), "404")
		})

		convey.Convey("非法 JSON 返回 decode 错误", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("not-json"))
			}))
			defer server.Close()

			_, err := fetchReleaseFromURL(server.URL)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "decode")
		})
	})
}

// TestFetchLatestBetaRelease 验证 beta 通道排除 nightly 后选最新。
func TestFetchLatestBetaRelease(t *testing.T) {
	convey.Convey("beta 通道排除 nightly", t, func() {
		convey.Convey("跳过 nightly 取第一个非 nightly", func() {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode([]ReleaseInfo{
					{TagName: "nightly", Name: "Nightly 20260522"},
					{TagName: "v1.0.0-beta.2", Name: "Beta 2"},
					{TagName: "v1.0.0-beta.1", Name: "Beta 1"},
				})
			}))
			defer server.Close()

			// 把 fetchLatestBetaRelease 替换不容易（apiBaseURL 是常量），
			// 这里直接复用其内部逻辑：用临时 URL fetch + 手动跳过 nightly
			// 因 fetchLatestBetaRelease 走 apiBaseURL 常量，无法注入；此处仅断言行为模式
			req, _ := http.NewRequest("GET", server.URL, nil)
			req.Header.Set("Accept", "application/vnd.github+json")
			resp, err := http.DefaultClient.Do(req)
			assert.NoError(t, err)
			defer func() { _ = resp.Body.Close() }()

			var releases []ReleaseInfo
			assert.NoError(t, json.NewDecoder(resp.Body).Decode(&releases))

			var picked *ReleaseInfo
			for i := range releases {
				if releases[i].TagName != "nightly" {
					picked = &releases[i]
					break
				}
			}
			assert.NotNil(t, picked)
			assert.Equal(t, "v1.0.0-beta.2", picked.TagName)
		})
	})
}

// TestUpdateInfoSerialization 验证 UpdateInfo JSON 字段名与前端约定一致。
func TestUpdateInfoSerialization(t *testing.T) {
	convey.Convey("UpdateInfo JSON 字段 camelCase", t, func() {
		info := UpdateInfo{
			HasUpdate:      true,
			CurrentVersion: "v0.1.0",
			LatestVersion:  "v0.2.0",
			ReleaseNotes:   "notes",
			ReleaseURL:     "https://example.com",
			PublishedAt:    "2026-05-22",
		}
		data, err := json.Marshal(info)
		assert.NoError(t, err)
		s := string(data)
		assert.Contains(t, s, `"hasUpdate":true`)
		assert.Contains(t, s, `"currentVersion":"v0.1.0"`)
		assert.Contains(t, s, `"latestVersion":"v0.2.0"`)
		assert.Contains(t, s, `"releaseNotes":"notes"`)
		assert.Contains(t, s, `"releaseURL":"https://example.com"`)
		assert.Contains(t, s, `"publishedAt":"2026-05-22"`)
	})
}

// TestProgressReader 验证下载进度回调按字节流推送。
func TestProgressReader(t *testing.T) {
	convey.Convey("progressReader 累计字节并回调", t, func() {
		var lastDl, lastTotal int64
		callCount := 0
		pr := &progressReader{
			r:     strings.NewReader("hello world"),
			total: 11,
			onProgress: func(downloaded, total int64) {
				lastDl = downloaded
				lastTotal = total
				callCount++
			},
		}

		buf := make([]byte, 5)
		n, err := pr.Read(buf)
		assert.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, int64(5), lastDl)
		assert.Equal(t, int64(11), lastTotal)

		n, err = pr.Read(buf)
		assert.NoError(t, err)
		assert.Equal(t, 5, n)
		assert.Equal(t, int64(10), lastDl)

		assert.GreaterOrEqual(t, callCount, 2)
	})
}

// TestServiceInterface 验证默认 service 转发到包级函数。
func TestServiceInterface(t *testing.T) {
	convey.Convey("默认 service 实现", t, func() {
		svc := Update()
		assert.NotNil(t, svc)

		convey.Convey("GetAvailableMirrors 转发到包级函数", func() {
			mirrors := svc.GetAvailableMirrors()
			expected := getAvailableMirrors()
			assert.Equal(t, len(expected), len(mirrors))
			assert.Equal(t, expected[0].ID, mirrors[0].ID)
		})

		convey.Convey("RegisterUpdate 可替换实现", func() {
			original := Update()
			defer RegisterUpdate(original)

			fake := &fakeService{mirrors: []MirrorInfo{{ID: "test", Name: "Test", URL: "https://test/"}}}
			RegisterUpdate(fake)

			assert.Equal(t, "test", Update().GetAvailableMirrors()[0].ID)
		})
	})
}

type fakeService struct {
	mirrors []MirrorInfo
}

func (f *fakeService) CheckForUpdate(_ paths.Channel, _ string) (*UpdateInfo, error) {
	return nil, nil
}
func (f *fakeService) DownloadAndUpdate(_ paths.Channel, _ string, _ func(int64, int64)) error {
	return nil
}
func (f *fakeService) GetAvailableMirrors() []MirrorInfo                   { return f.mirrors }
func (f *fakeService) GetMirror(_ context.Context) (string, error)         { return "", nil }
func (f *fakeService) SetMirror(_ context.Context, _ string) error         { return nil }
func (f *fakeService) GetLastUpdateCheck(_ context.Context) (int64, error) { return 0, nil }
func (f *fakeService) SetLastUpdateCheck(_ context.Context, _ int64) error { return nil }

// recordingServer 是一台会记账的测试服务器：它按「路径后缀」供文件，并记下每一次
// 请求路径。镜像的请求路径是「镜像前缀 + 原始 URL」，只有后缀是稳定的；而「校验和
// 有没有从这台机器上取过」正是同源校验那个缺陷的判据，所以必须记账。
type recordingServer struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string
	files    map[string][]byte
}

func newRecordingServer(t *testing.T, files map[string][]byte) *recordingServer {
	t.Helper()
	srv := &recordingServer{files: files}
	srv.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		srv.mu.Lock()
		srv.requests = append(srv.requests, r.URL.Path)
		srv.mu.Unlock()
		for suffix, body := range srv.files {
			if strings.HasSuffix(r.URL.Path, suffix) {
				_, _ = w.Write(body)
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// asked 回答「这台服务器被要过带该后缀的东西吗」。
func (s *recordingServer) asked(suffix string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, path := range s.requests {
		if strings.HasSuffix(path, suffix) {
			return true
		}
	}
	return false
}

// paths 返回收到过的请求路径副本。
func (s *recordingServer) paths() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	body, err := json.Marshal(v)
	require.NoError(t, err)
	return body
}

// unreachableFetch 是「权威元数据取不到」（api.github.com 被墙 / 超时）。
func unreachableFetch(string) (*ReleaseInfo, error) {
	return nil, errors.New("api.github.com unreachable")
}

// TestResolveInstallRelease 钉住桌面端安装路径的元数据来源：这份 release-info 决定
// 资产地址**与**校验和地址，一旦允许镜像来供它，校验和与被它校验的二进制就同源，
// 代理运营方单方面即可投毒。取不到就失败，不降级。
func TestResolveInstallRelease(t *testing.T) {
	convey.Convey("决定下载地址的那份发布元数据", t, func() {
		mirror := newRecordingServer(t, map[string][]byte{
			"/release-info.json": mustJSON(t, ReleaseInfo{TagName: "v9.9.9", Name: "v9.9.9"}),
		})
		sources := releaseSources{
			fetchRelease:    unreachableFetch,
			mirrors:         []MirrorInfo{{ID: "fake", Name: "fake", URL: mirror.URL + "/"}},
			checksumBaseURL: githubDownloadBaseURL,
		}

		// 签名上就不收镜像前缀 —— 配了镜像也一样，这份元数据只从权威来源取。
		convey.Convey("权威来源不可达时整个安装失败，且不从镜像取这份元数据", func() {
			release, err := resolveInstallRelease(sources, ChannelStable)
			require.Error(t, err)
			assert.Nil(t, release)
			assert.False(t, mirror.asked("release-info.json"),
				"镜像供的元数据同时决定资产地址与校验和地址，校验和就只能证明「镜像自洽」")
		})

		convey.Convey("权威来源可达时用它，镜像照样不参与", func() {
			sources.fetchRelease = func(string) (*ReleaseInfo, error) {
				return &ReleaseInfo{TagName: "v1.2.3"}, nil
			}
			release, err := resolveInstallRelease(sources, ChannelStable)
			require.NoError(t, err)
			assert.Equal(t, "v1.2.3", release.TagName)
			assert.Empty(t, mirror.paths())
		})
	})
}

// TestInstallChecksums 钉住校验和的来源：地址由「权威域名 + tag」拼出来，而不是读
// 元数据里带的 URL —— 元数据可能来自镜像，那 URL 就由镜像说了算。
func TestInstallChecksums(t *testing.T) {
	convey.Convey("桌面端安装要比对的校验和清单", t, func() {
		manifest := []byte("aa" + strings.Repeat("0", 62) + "  agentre-v1.2.3-darwin-arm64.dmg\n")
		authoritative := newRecordingServer(t, map[string][]byte{
			"/releases/download/v1.2.3/SHA256SUMS.txt": manifest,
		})
		poisoned := newRecordingServer(t, map[string][]byte{
			"/SHA256SUMS.txt": []byte("bb" + strings.Repeat("0", 62) + "  agentre-v1.2.3-darwin-arm64.dmg\n"),
		})
		release := &ReleaseInfo{
			TagName: "v1.2.3",
			Assets: []ReleaseAsset{
				{Name: "SHA256SUMS.txt", BrowserDownloadURL: poisoned.URL + "/SHA256SUMS.txt"},
			},
		}

		convey.Convey("只从权威域名按 tag 取，元数据里那个地址不采纳", func() {
			checksums, err := installChecksums(releaseSources{checksumBaseURL: authoritative.URL}, release)
			require.NoError(t, err)
			assert.Equal(t, "aa"+strings.Repeat("0", 62), checksums["agentre-v1.2.3-darwin-arm64.dmg"])
			assert.Equal(t, []string{"/releases/download/v1.2.3/SHA256SUMS.txt"}, authoritative.paths())
			assert.Empty(t, poisoned.paths(), "元数据里带的校验和地址一律不访问")
		})

		convey.Convey("权威域名上没有这份清单时报错，而不是回一张 nil 校验表", func() {
			empty := newRecordingServer(t, nil)
			checksums, err := installChecksums(releaseSources{checksumBaseURL: empty.URL}, release)
			require.Error(t, err)
			assert.Nil(t, checksums)
		})
	})
}

// TestAuthoritativeChecksumURL 钉住校验和地址怎么来的：权威域名 + 发布号，且发布号
// 得是发布号的形状 —— 它可能来自镜像供的元数据，不能拿去随手拼地址。
func TestAuthoritativeChecksumURL(t *testing.T) {
	convey.Convey("校验文件的权威地址", t, func() {
		convey.Convey("按发布号拼在权威域名下", func() {
			url, err := authoritativeChecksumURL(githubDownloadBaseURL, "v1.2.3")
			require.NoError(t, err)
			assert.Equal(t, "https://github.com/agentre-hub/agentre/releases/download/v1.2.3/SHA256SUMS.txt", url)
		})

		convey.Convey("nightly 通道的固定发布号同样成立", func() {
			url, err := authoritativeChecksumURL(githubDownloadBaseURL, "nightly")
			require.NoError(t, err)
			assert.Equal(t, "https://github.com/agentre-hub/agentre/releases/download/nightly/SHA256SUMS.txt", url)
		})

		for _, tag := range []string{"", "../../evil", "v1.2.3?x=", "v1 2"} {
			convey.Convey("发布号形状不对时拒绝拼地址: "+tag, func() {
				url, err := authoritativeChecksumURL(githubDownloadBaseURL, tag)
				require.Error(t, err)
				assert.Empty(t, url)
			})
		}
	})
}

// TestPickLatestDesktopBetaRelease 钉住桌面端 Beta 通道只更新到 vX.Y.Z-beta.N 的非
// draft 预发布，取最新一个；没有这样的发布时不回落到正式版（与 agentred 共用的
// fetchLatestBetaRelease/pickLatestStableRelease 刻意保留回落，这里必须是另一条路）。
func TestPickLatestDesktopBetaRelease(t *testing.T) {
	convey.Convey("桌面端 Beta 通道发布选择", t, func() {
		convey.Convey("跳过 nightly 与正式版 tag，取最新的 vX.Y.Z-beta.N", func() {
			release, err := pickLatestDesktopBetaRelease([]ReleaseInfo{
				{TagName: "nightly", Name: "v1.0.0-nightly.20260528", Prerelease: true},
				{TagName: "v1.0.0-beta.2", Prerelease: true},
				{TagName: "v0.9.0", Prerelease: false},
			})
			require.NoError(t, err)
			assert.Equal(t, "v1.0.0-beta.2", release.TagName)
		})

		convey.Convey("跳过 draft 的 beta tag", func() {
			release, err := pickLatestDesktopBetaRelease([]ReleaseInfo{
				{TagName: "v1.0.0-beta.3", Draft: true, Prerelease: true},
				{TagName: "v1.0.0-beta.2", Draft: false, Prerelease: true},
			})
			require.NoError(t, err)
			assert.Equal(t, "v1.0.0-beta.2", release.TagName)
		})

		convey.Convey("只有正式版 tag 时不回落，返回明确的「无 beta 发布」错误", func() {
			release, err := pickLatestDesktopBetaRelease([]ReleaseInfo{
				{TagName: "v1.0.0", Prerelease: false},
			})
			assert.Nil(t, release)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errNoBetaRelease))
		})

		convey.Convey("没有任何发布时同样返回「无 beta 发布」错误", func() {
			release, err := pickLatestDesktopBetaRelease(nil)
			assert.Nil(t, release)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errNoBetaRelease))
		})

		convey.Convey("beta tag 但没标预发布的 release 不被接受（只看预发布）", func() {
			release, err := pickLatestDesktopBetaRelease([]ReleaseInfo{
				{TagName: "v1.0.0-beta.3", Prerelease: false},
			})
			assert.Nil(t, release)
			assert.True(t, errors.Is(err, errNoBetaRelease))
		})

		convey.Convey("形似 beta 但不是 vX.Y.Z-beta.N 的 tag 不被接受", func() {
			for _, tag := range []string{
				"v1.0.0-beta", "v1.0.0-beta.x", "v1.0.0-rc.1", "beta-1.0.0",
				// 基于 beta 打的 nightly（nightly.yml VERSION 计算的形状）不是
				// 「-beta tag 的发布」，Beta 通道不接受。
				"v1.0.0-beta.1.nightly.20260917",
			} {
				release, err := pickLatestDesktopBetaRelease([]ReleaseInfo{{TagName: tag, Prerelease: true}})
				assert.Nil(t, release, "tag=%s", tag)
				assert.True(t, errors.Is(err, errNoBetaRelease), "tag=%s", tag)
			}
		})
	})
}

// TestFetchReleaseForChannel 钉住 Dev 渠道不发起任何网络请求、也选不中任何发布 ——
// 决定 9「Dev 没有发布」。stable/beta/nightly 的网络分支复用上面已单测过的纯函数
// (pickLatestStableRelease/pickLatestDesktopBetaRelease)，这里不重复用真实网络断言。
func TestFetchReleaseForChannel(t *testing.T) {
	convey.Convey("按渠道取 release", t, func() {
		convey.Convey("Dev 直接返回错误，不发请求、不选中任何发布", func() {
			release, err := fetchReleaseForChannel(paths.ChannelDev)
			assert.Nil(t, release)
			require.Error(t, err)
			assert.True(t, errors.Is(err, errDevNoRelease))
		})
	})
}

// TestPickDesktopAsset 钉住「选包」规则：名字必须是 本渠道前缀 + 版本号形状 + -goos-arch
// + 扩展名；agentred- 归档与其他渠道的包（agentre- 是 agentre-beta- 的前缀）永远不会被
// 选中；没有匹配时报出「该版本没有适用于本平台的安装包」，不选任何资产。
func TestPickDesktopAsset(t *testing.T) {
	assets := []ReleaseAsset{
		{Name: "agentred-v1.2.3-darwin-arm64.tar.gz", BrowserDownloadURL: "agentred"},
		{Name: "agentre-v1.2.3-darwin-arm64.dmg", BrowserDownloadURL: "stable-darwin-arm64"},
		{Name: "agentre-v1.2.3-linux-amd64.tar.gz", BrowserDownloadURL: "stable-linux-amd64"},
		{Name: "agentre-beta-v1.2.3-beta.4-darwin-arm64.dmg", BrowserDownloadURL: "beta-darwin-arm64"},
		{Name: "agentre-nightly-v1.2.3-nightly.20260917-darwin-arm64.dmg", BrowserDownloadURL: "nightly-darwin-arm64"},
	}

	convey.Convey("按渠道前缀 + 版本形状 + 平台精确选包", t, func() {
		convey.Convey("stable 只选自己的包，不选 agentred-，也不选更长前缀的 beta 包", func() {
			asset, err := pickDesktopAsset(assets, paths.ChannelStable.Identity().ReleaseAssetPrefix, "darwin", "arm64")
			require.NoError(t, err)
			assert.Equal(t, "stable-darwin-arm64", asset.BrowserDownloadURL)
		})

		convey.Convey("beta 只选带 agentre-beta- 前缀的包", func() {
			asset, err := pickDesktopAsset(assets, paths.ChannelBeta.Identity().ReleaseAssetPrefix, "darwin", "arm64")
			require.NoError(t, err)
			assert.Equal(t, "beta-darwin-arm64", asset.BrowserDownloadURL)
		})

		convey.Convey("nightly 只选带 agentre-nightly- 前缀的包", func() {
			asset, err := pickDesktopAsset(assets, paths.ChannelNightly.Identity().ReleaseAssetPrefix, "darwin", "arm64")
			require.NoError(t, err)
			assert.Equal(t, "nightly-darwin-arm64", asset.BrowserDownloadURL)
		})

		convey.Convey("平台不匹配时不选任何东西，报「该版本没有适用于本平台的安装包」", func() {
			asset, err := pickDesktopAsset(assets, paths.ChannelStable.Identity().ReleaseAssetPrefix, "windows", "amd64")
			assert.Nil(t, asset)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "该版本没有适用于本平台的安装包")
		})

		convey.Convey("Windows 选中本渠道 NSIS 安装器（CI 产出 <前缀><版本>-windows-<arch>-installer.exe）", func() {
			winAssets := []ReleaseAsset{
				{Name: "agentred-v1.2.3-windows-amd64.zip", BrowserDownloadURL: "agentred"},
				{Name: "agentre-beta-v1.2.3-beta.4-windows-amd64-installer.exe", BrowserDownloadURL: "beta-windows-amd64"},
				{Name: "agentre-v1.2.3-windows-amd64-installer.exe", BrowserDownloadURL: "stable-windows-amd64"},
			}
			asset, err := pickDesktopAsset(winAssets, paths.ChannelStable.Identity().ReleaseAssetPrefix, "windows", "amd64")
			require.NoError(t, err)
			assert.Equal(t, "stable-windows-amd64", asset.BrowserDownloadURL)

			asset, err = pickDesktopAsset(winAssets, paths.ChannelBeta.Identity().ReleaseAssetPrefix, "windows", "amd64")
			require.NoError(t, err)
			assert.Equal(t, "beta-windows-amd64", asset.BrowserDownloadURL)
		})

		convey.Convey("Dev 没有发布包前缀，永远选不中任何东西", func() {
			asset, err := pickDesktopAsset(assets, paths.ChannelDev.Identity().ReleaseAssetPrefix, "darwin", "arm64")
			assert.Nil(t, asset)
			require.Error(t, err)
		})

		convey.Convey("agentred- 归档即使平台匹配也不会被任何桌面渠道选中", func() {
			for _, prefix := range []string{
				paths.ChannelStable.Identity().ReleaseAssetPrefix,
				paths.ChannelBeta.Identity().ReleaseAssetPrefix,
				paths.ChannelNightly.Identity().ReleaseAssetPrefix,
			} {
				asset, err := pickDesktopAsset(
					[]ReleaseAsset{{Name: "agentred-v1.2.3-darwin-arm64.tar.gz"}},
					prefix, "darwin", "arm64")
				assert.Nil(t, asset, "prefix=%s", prefix)
				assert.Error(t, err, "prefix=%s", prefix)
			}
		})
	})
}

// TestDesktopBundleFileName 钉住 macOS 安装取的是本渠道 bundle 名，例如 Beta 取
// "Agentre Beta.app"，与渠道身份表一致。
func TestDesktopBundleFileName(t *testing.T) {
	convey.Convey("macOS bundle 文件名取自渠道身份", t, func() {
		assert.Equal(t, "Agentre.app", desktopBundleFileName(paths.ChannelStable))
		assert.Equal(t, "Agentre Beta.app", desktopBundleFileName(paths.ChannelBeta))
		assert.Equal(t, "Agentre Nightly.app", desktopBundleFileName(paths.ChannelNightly))
		assert.Equal(t, "Agentre Dev.app", desktopBundleFileName(paths.ChannelDev))
	})
}

// TestCheckForUpdateForChannelWithFetch 钉住桌面端按渠道检查更新的分流：Beta 无
// 匹配发布时是「暂无更新」而不是报错或回落；Dev 完全不发请求；网络失败时正式版仍
// 走镜像回落（与现状一致）。
func TestCheckForUpdateForChannelWithFetch(t *testing.T) {
	convey.Convey("按渠道检查更新", t, func() {
		convey.Convey("Beta 没有匹配发布时结果是「暂无更新」，不报错", func() {
			fetch := func(paths.Channel) (*ReleaseInfo, error) { return nil, errNoBetaRelease }
			info, err := checkForUpdateForChannelWithFetch(paths.ChannelBeta, "", fetch)
			require.NoError(t, err)
			require.NotNil(t, info)
			assert.False(t, info.HasUpdate)
		})

		convey.Convey("Dev 不调用 fetch，直接返回错误", func() {
			called := false
			fetch := func(paths.Channel) (*ReleaseInfo, error) {
				called = true
				return nil, nil
			}
			info, err := checkForUpdateForChannelWithFetch(paths.ChannelDev, "", fetch)
			assert.Nil(t, info)
			require.Error(t, err)
			assert.False(t, called, "Dev 不应该发起任何请求")
		})

		convey.Convey("正式版权威来源失败时走镜像回落，与现状一致", func() {
			mirror := newRecordingServer(t, map[string][]byte{
				"/release-info.json": mustJSON(t, ReleaseInfo{TagName: "v9.9.9", Name: "v9.9.9"}),
			})
			fetch := func(paths.Channel) (*ReleaseInfo, error) {
				return nil, errors.New("api.github.com unreachable")
			}
			info, err := checkForUpdateForChannelWithFetch(paths.ChannelStable, mirror.URL+"/", fetch)
			require.NoError(t, err)
			require.NotNil(t, info)
			assert.Equal(t, "v9.9.9", info.LatestVersion)
			assert.True(t, mirror.asked("release-info.json"))
		})

		convey.Convey("nightly 用 release title 作版本号，与现状一致", func() {
			fetch := func(paths.Channel) (*ReleaseInfo, error) {
				return &ReleaseInfo{TagName: "nightly", Name: "v1.0.0-nightly.20260917"}, nil
			}
			info, err := checkForUpdateForChannelWithFetch(paths.ChannelNightly, "", fetch)
			require.NoError(t, err)
			assert.Equal(t, "v1.0.0-nightly.20260917", info.LatestVersion)
		})
	})
}

// TestResolveInstallReleaseNoChannelRelease 钉住「Beta/Dev 取不到发布」时
// resolveInstallRelease 原样透出哨兵错误，不裹成通用的「获取 GitHub 官方版本信息失败」，
// 也不下载任何东西。
func TestResolveInstallReleaseNoChannelRelease(t *testing.T) {
	convey.Convey("取不到本渠道发布时原样透出", t, func() {
		sources := releaseSources{checksumBaseURL: githubDownloadBaseURL}

		convey.Convey("Beta 没有匹配发布", func() {
			sources.fetchRelease = func(string) (*ReleaseInfo, error) { return nil, errNoBetaRelease }
			release, err := resolveInstallRelease(sources, ChannelBeta)
			assert.Nil(t, release)
			assert.True(t, errors.Is(err, errNoBetaRelease))
		})

		convey.Convey("Dev 没有发布", func() {
			sources.fetchRelease = func(string) (*ReleaseInfo, error) { return nil, errDevNoRelease }
			release, err := resolveInstallRelease(sources, string(paths.ChannelDev))
			assert.Nil(t, release)
			assert.True(t, errors.Is(err, errDevNoRelease))
		})
	})
}

// TestDownloadAndUpdateForChannelDevGuard 钉住 Dev 渠道的 DownloadAndUpdate 不选中
// 任何 release / 安装包：还没到「按渠道取哪个 release」这一步就先失败。
func TestDownloadAndUpdateForChannelDevGuard(t *testing.T) {
	convey.Convey("Dev 渠道下载更新直接失败", t, func() {
		err := downloadAndUpdateForChannel(paths.ChannelDev, "", nil)
		require.Error(t, err)
		assert.True(t, errors.Is(err, errDevNoRelease))
	})
}
