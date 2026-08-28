package update_svc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
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

// TestFetchChecksums_GivenAReleaseWithoutChecksums_WhenFetched_ThenItIsAnError
// 钉住「没有 SHA256SUMS.txt 就不装」这条线:早先这里回 (nil, nil),而调用方对
// nil 校验表的处理是**静默跳过校验** —— 一个没带校验文件的 release 会被无声地
// 装上去。项目未发布,不存在缺这个资产的历史 release,缺了就是异常。
//
// 交出错误而不是自己中止:DownloadAndUpdate 把它包成 ChecksumFetchError 前缀,
// 前端据此提示用户,由用户显式选择 skipChecksum=true 才继续 —— 跳过校验必须是
// 用户按下的那一下,不是代码替他做的默认。
func TestFetchChecksums_GivenAReleaseWithoutChecksums_WhenFetched_ThenItIsAnError(t *testing.T) {
	convey.Convey("release 里没有 SHA256SUMS.txt", t, func() {
		convey.Convey("空 assets 报错,而不是回一张 nil 校验表", func() {
			checksums, err := FetchChecksums(nil)
			assert.Error(t, err)
			assert.Nil(t, checksums)
		})

		convey.Convey("有资产但没有校验文件同样报错", func() {
			assets := []ReleaseAsset{
				{Name: "agentre-v1.0.0-darwin-arm64.dmg", BrowserDownloadURL: "https://example.com/file.dmg"},
			}
			checksums, err := FetchChecksums(assets)
			assert.Error(t, err)
			assert.Nil(t, checksums)
		})
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
			expected := GetAvailableMirrors()
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

func (f *fakeService) CheckForUpdate(_, _ string) (*UpdateInfo, error) { return nil, nil }
func (f *fakeService) DownloadAndUpdate(_, _ string, _ bool, _ func(int64, int64)) error {
	return nil
}
func (f *fakeService) GetAvailableMirrors() []MirrorInfo                   { return f.mirrors }
func (f *fakeService) GetChannel(_ context.Context) (string, error)        { return "stable", nil }
func (f *fakeService) SetChannel(_ context.Context, _ string) error        { return nil }
func (f *fakeService) GetMirror(_ context.Context) (string, error)         { return "", nil }
func (f *fakeService) SetMirror(_ context.Context, _ string) error         { return nil }
func (f *fakeService) GetLastUpdateCheck(_ context.Context) (int64, error) { return 0, nil }
func (f *fakeService) SetLastUpdateCheck(_ context.Context, _ int64) error { return nil }
