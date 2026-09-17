package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAgentredDataDir_EnvOverride(t *testing.T) {
	t.Setenv("AGENTRED_DATA_DIR", "/tmp/agentred-custom")
	dir, err := AgentredDataDir()
	require.NoError(t, err)
	assert.Equal(t, "/tmp/agentred-custom", dir)
}

func TestAgentredDataDir_Default(t *testing.T) {
	t.Setenv("AGENTRED_DATA_DIR", "")
	dir, err := AgentredDataDir()
	require.NoError(t, err)
	assert.True(t, strings.HasSuffix(dir, AppNameAgentred),
		"got %q want suffix %q", dir, AppNameAgentred)
	base, _ := os.UserConfigDir()
	assert.Equal(t, filepath.Join(base, AppNameAgentred), dir)
}

func TestAgentredDataDir_IsolatedFromAgentre(t *testing.T) {
	t.Setenv("AGENTRE_DATA_DIR", "")
	t.Setenv("AGENTRED_DATA_DIR", "")
	for _, c := range AllChannels() {
		SetBuildChannelForTest(t, string(c))
		d1, err := AppDataDir()
		require.NoError(t, err)
		d2, _ := AgentredDataDir()
		assert.NotEqual(t, d1, d2, "agentred dir must not collide with the %s channel dir", c)
	}
}

func TestIsDevMode(t *testing.T) {
	t.Setenv("devserver", "http://localhost:34115")
	assert.True(t, IsDevMode(), "devserver set => dev mode")

	t.Setenv("devserver", "  ")
	assert.False(t, IsDevMode(), "blank devserver => not dev mode")

	t.Setenv("devserver", "")
	assert.False(t, IsDevMode(), "unset devserver => not dev mode")
}

func TestParseChannel(t *testing.T) {
	Convey("ParseChannel", t, func() {
		Convey("when given each of the four build flags, then it returns that channel", func() {
			for flag, want := range map[string]Channel{
				"stable":  ChannelStable,
				"beta":    ChannelBeta,
				"nightly": ChannelNightly,
				"dev":     ChannelDev,
			} {
				got, err := ParseChannel(flag)
				So(err, ShouldBeNil)
				So(got, ShouldEqual, want)
			}
		})

		Convey("when the flag is empty, then the build belongs to Dev", func() {
			got, err := ParseChannel("")
			So(err, ShouldBeNil)
			So(got, ShouldEqual, ChannelDev)
		})

		Convey("when the flag is not one of the four values, then it errors naming the value and the allowed values", func() {
			for _, flag := range []string{"release", "Stable", " beta", "rc"} {
				_, err := ParseChannel(flag)
				So(err, ShouldNotBeNil)
				So(err.Error(), ShouldContainSubstring, `"`+flag+`"`)
				for _, allowed := range []string{"stable", "beta", "nightly", "dev"} {
					So(err.Error(), ShouldContainSubstring, allowed)
				}
			}
		})
	})
}

func TestCurrentChannel(t *testing.T) {
	Convey("CurrentChannel", t, func() {
		Convey("when the build carries no channel flag, then it is Dev even outside wails dev", func() {
			SetBuildChannelForTest(t, "")
			t.Setenv("devserver", "")
			got, err := CurrentChannel()
			So(err, ShouldBeNil)
			So(got, ShouldEqual, ChannelDev)
		})

		Convey("when the build is flagged stable, then wails dev does not turn it into Dev", func() {
			SetBuildChannelForTest(t, "stable")
			t.Setenv("devserver", "http://localhost:34115")
			got, err := CurrentChannel()
			So(err, ShouldBeNil)
			So(got, ShouldEqual, ChannelStable)
		})

		Convey("when the build flag is invalid, then it errors", func() {
			SetBuildChannelForTest(t, "release")
			_, err := CurrentChannel()
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `"release"`)
		})
	})
}

func TestAllChannels(t *testing.T) {
	Convey("when listing channels, then all four are returned in a stable order", t, func() {
		So(AllChannels(), ShouldResemble, []Channel{ChannelStable, ChannelBeta, ChannelNightly, ChannelDev})
	})
}

func TestChannelIdentity(t *testing.T) {
	Convey("Channel.Identity", t, func() {
		Convey("when asked for each channel, then it matches the channel identity table", func() {
			So(ChannelStable.Identity(), ShouldResemble, Identity{
				Channel: ChannelStable, DisplayName: "Agentre", BundleID: "com.agentrehub.agentre",
				DataDirName: "agentre", KeychainService: "agentre", ReleaseAssetPrefix: "agentre-", LinuxCommand: "agentre",
			})
			So(ChannelBeta.Identity(), ShouldResemble, Identity{
				Channel: ChannelBeta, DisplayName: "Agentre Beta", BundleID: "com.agentrehub.agentre.beta",
				DataDirName: "agentre-beta", KeychainService: "agentre-beta", ReleaseAssetPrefix: "agentre-beta-", LinuxCommand: "agentre-beta",
			})
			So(ChannelNightly.Identity(), ShouldResemble, Identity{
				Channel: ChannelNightly, DisplayName: "Agentre Nightly", BundleID: "com.agentrehub.agentre.nightly",
				DataDirName: "agentre-nightly", KeychainService: "agentre-nightly", ReleaseAssetPrefix: "agentre-nightly-", LinuxCommand: "agentre-nightly",
			})
			So(ChannelDev.Identity(), ShouldResemble, Identity{
				Channel: ChannelDev, DisplayName: "Agentre Dev", BundleID: "com.agentrehub.agentre.dev",
				DataDirName: "agentre-dev", KeychainService: "agentre-dev", ReleaseAssetPrefix: "", LinuxCommand: "agentre-dev",
			})
		})

		Convey("when comparing channels, then no two share a display name, bundle id, data dir or keychain service", func() {
			fields := map[string]func(Identity) string{
				"DisplayName":     func(id Identity) string { return id.DisplayName },
				"BundleID":        func(id Identity) string { return id.BundleID },
				"DataDirName":     func(id Identity) string { return id.DataDirName },
				"KeychainService": func(id Identity) string { return id.KeychainService },
			}
			for _, field := range fields {
				seen := map[string]bool{}
				for _, c := range AllChannels() {
					v := field(c.Identity())
					So(v, ShouldNotBeEmpty)
					So(seen[v], ShouldBeFalse)
					seen[v] = true
				}
			}
		})
	})
}

func TestAppDataDir(t *testing.T) {
	base, err := os.UserConfigDir()
	require.NoError(t, err)

	Convey("AppDataDir", t, func() {
		Convey("when built for each channel without an override, then it uses that channel's data dir name", func() {
			t.Setenv("AGENTRE_DATA_DIR", "")
			for _, c := range AllChannels() {
				SetBuildChannelForTest(t, string(c))
				dir, err := AppDataDir()
				So(err, ShouldBeNil)
				So(dir, ShouldEqual, filepath.Join(base, c.Identity().DataDirName))
			}
		})

		Convey("when a stable build runs under wails dev, then the data dir is still the stable one", func() {
			t.Setenv("AGENTRE_DATA_DIR", "")
			t.Setenv("devserver", "http://localhost:34115")
			SetBuildChannelForTest(t, "stable")
			dir, err := AppDataDir()
			So(err, ShouldBeNil)
			So(dir, ShouldEqual, filepath.Join(base, "agentre"))
		})

		Convey("when an unflagged build runs outside wails dev, then it never lands on the stable data dir", func() {
			t.Setenv("AGENTRE_DATA_DIR", "")
			t.Setenv("devserver", "")
			SetBuildChannelForTest(t, "")
			dir, err := AppDataDir()
			So(err, ShouldBeNil)
			So(dir, ShouldEqual, filepath.Join(base, "agentre-dev"))
		})

		Convey("when AGENTRE_DATA_DIR is set, then the override wins for a valid channel", func() {
			t.Setenv("AGENTRE_DATA_DIR", "/tmp/agentre-custom")
			SetBuildChannelForTest(t, "beta")
			dir, err := AppDataDir()
			So(err, ShouldBeNil)
			So(dir, ShouldEqual, "/tmp/agentre-custom")
		})

		Convey("when the build flag is invalid, then it errors even with an override, naming the value and allowed values", func() {
			t.Setenv("AGENTRE_DATA_DIR", "/tmp/agentre-custom")
			SetBuildChannelForTest(t, "release")
			dir, err := AppDataDir()
			So(dir, ShouldBeEmpty)
			So(err, ShouldNotBeNil)
			So(err.Error(), ShouldContainSubstring, `"release"`)
			So(err.Error(), ShouldContainSubstring, "stable, beta, nightly, dev")
		})
	})
}

func TestDefaultAppDataDir(t *testing.T) {
	Convey("when resolving a channel's default dir under a config root, then it joins the identity data dir name", t, func() {
		base := t.TempDir()
		So(DefaultAppDataDir(base, ChannelStable), ShouldEqual, filepath.Join(base, "agentre"))
		So(DefaultAppDataDir(base, ChannelBeta), ShouldEqual, filepath.Join(base, "agentre-beta"))
		So(DefaultAppDataDir(base, ChannelNightly), ShouldEqual, filepath.Join(base, "agentre-nightly"))
		So(DefaultAppDataDir(base, ChannelDev), ShouldEqual, filepath.Join(base, "agentre-dev"))
	})
}
