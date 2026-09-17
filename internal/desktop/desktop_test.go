package desktop

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/agentre-hub/agentre/internal/app"
	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/pkg/paths"

	"github.com/wailsapp/wails/v2/pkg/options"
)

func TestRunOrdersBootstrapCompositionAndWails(t *testing.T) {
	originalBootstrap := bootstrapDesktop
	originalWailsRun := runWails
	t.Cleanup(func() {
		bootstrapDesktop = originalBootstrap
		runWails = originalWailsRun
	})

	var order []string
	runtime := &bootstrap.Runtime{}
	bootstrapDesktop = func(context.Context) (*bootstrap.Runtime, error) {
		order = append(order, "bootstrap")
		return runtime, nil
	}
	runWails = func(_ *options.App) error {
		order = append(order, "wails")
		return nil
	}

	err := Run(context.Background(), Options{
		Assets:      fstest.MapFS{},
		GOOS:        "darwin",
		RuntimeMode: app.RuntimeModeHeadless,
		AfterBootstrap: func(_ context.Context, got *bootstrap.Runtime) error {
			if got != runtime {
				t.Fatalf("AfterBootstrap runtime = %p, want %p", got, runtime)
			}
			order = append(order, "composition")
			return nil
		},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{"bootstrap", "composition", "wails"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestRunBindsTheSuppliedAppInstance(t *testing.T) {
	originalBootstrap := bootstrapDesktop
	originalWailsRun := runWails
	t.Cleanup(func() {
		bootstrapDesktop = originalBootstrap
		runWails = originalWailsRun
	})

	supplied := app.NewApp(app.RuntimeModeHeadless)
	bootstrapDesktop = func(context.Context) (*bootstrap.Runtime, error) {
		return &bootstrap.Runtime{}, nil
	}
	runWails = func(opts *options.App) error {
		if len(opts.Bind) != 1 || opts.Bind[0] != supplied {
			t.Fatalf("Bind = %#v, want supplied app %p", opts.Bind, supplied)
		}
		return nil
	}

	if err := Run(context.Background(), Options{App: supplied, Assets: fstest.MapFS{}, GOOS: "darwin"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestRunDoesNotStartWailsWhenCompositionFails(t *testing.T) {
	originalBootstrap := bootstrapDesktop
	originalWailsRun := runWails
	t.Cleanup(func() {
		bootstrapDesktop = originalBootstrap
		runWails = originalWailsRun
	})

	bootstrapDesktop = func(context.Context) (*bootstrap.Runtime, error) {
		return &bootstrap.Runtime{}, nil
	}
	runWails = func(_ *options.App) error {
		t.Fatal("wails.Run must not be called after composition failure")
		return nil
	}
	installErr := errors.New("install failed")

	err := Run(context.Background(), Options{
		Assets:      fstest.MapFS{},
		GOOS:        "darwin",
		RuntimeMode: app.RuntimeModeHeadless,
		AfterBootstrap: func(context.Context, *bootstrap.Runtime) error {
			return installErr
		},
	})
	if !errors.Is(err, installErr) {
		t.Fatalf("Run error = %v, want %v", err, installErr)
	}
}

func TestNewWailsOptionsPreservesInteractiveDesktopBehavior(t *testing.T) {
	var assets fs.FS = fstest.MapFS{}

	t.Run("Given production mode Then single-instance locking is data-dir scoped", func(t *testing.T) {
		t.Setenv("devserver", "")
		opts := newWailsOptions(app.NewApp(app.RuntimeModeInteractive), assets, "darwin", "/tmp/agentre-test")
		if opts.SingleInstanceLock == nil {
			t.Fatal("SingleInstanceLock is nil")
		}
		if opts.SingleInstanceLock.UniqueId != singleInstanceUniqueID("/tmp/agentre-test") {
			t.Fatalf("SingleInstanceLock.UniqueId = %q", opts.SingleInstanceLock.UniqueId)
		}
		if opts.OnBeforeClose == nil {
			t.Fatal("OnBeforeClose must be wired")
		}
		if !opts.StartHidden {
			t.Fatal("desktop must start hidden until the interactive frontend restores and shows it")
		}
	})

	t.Run("Given Wails dev Then the lock is omitted", func(t *testing.T) {
		t.Setenv("devserver", "localhost:34115")
		opts := newWailsOptions(app.NewApp(app.RuntimeModeInteractive), assets, "darwin", "/tmp/agentre-test")
		if opts.SingleInstanceLock != nil {
			t.Fatal("SingleInstanceLock should be nil in Wails dev mode")
		}
	})
}

func TestNewWailsOptionsTitleComesFromChannelIdentity(t *testing.T) {
	var assets fs.FS = fstest.MapFS{}

	for _, tc := range []struct {
		channel   paths.Channel
		devserver string
		want      string
	}{
		{paths.ChannelStable, "", "Agentre"},
		{paths.ChannelBeta, "", "Agentre Beta"},
		{paths.ChannelNightly, "", "Agentre Nightly"},
		{paths.ChannelDev, "", "Agentre Dev"},
		// Wails dev is a run mode, not a channel: it must not rename a stable build.
		{paths.ChannelStable, "localhost:34115", "Agentre"},
	} {
		t.Run("Given channel "+string(tc.channel)+" and devserver "+tc.devserver+" Then title is "+tc.want, func(t *testing.T) {
			t.Setenv("devserver", tc.devserver)
			paths.SetBuildChannelForTest(t, string(tc.channel))
			opts := newWailsOptions(app.NewApp(app.RuntimeModeInteractive), assets, "darwin", "/tmp/agentre-test")
			if opts.Title != tc.want {
				t.Fatalf("Title = %q, want %q", opts.Title, tc.want)
			}
		})
	}
}

func TestNewWailsOptionsSeparatesWebView2StoragePerChannel(t *testing.T) {
	var assets fs.FS = fstest.MapFS{}
	original := userConfigDir
	t.Cleanup(func() { userConfigDir = original })
	root := filepath.Join(t.TempDir(), "Roaming")
	userConfigDir = func() (string, error) { return root, nil }

	seen := map[string]paths.Channel{}
	for _, c := range paths.AllChannels() {
		t.Run("Given channel "+string(c)+" on windows Then WebView2 data lives under the channel data dir name", func(t *testing.T) {
			paths.SetBuildChannelForTest(t, string(c))
			// A data-dir override only moves the database, not the channel's other identity.
			opts := newWailsOptions(app.NewApp(app.RuntimeModeInteractive), assets, "windows", "/tmp/override")
			if opts.Windows == nil {
				t.Fatal("Windows options are nil")
			}
			want := filepath.Join(root, c.Identity().DataDirName, "WebView2")
			if opts.Windows.WebviewUserDataPath != want {
				t.Fatalf("WebviewUserDataPath = %q, want %q", opts.Windows.WebviewUserDataPath, want)
			}
			if other, dup := seen[want]; dup {
				t.Fatalf("channels %s and %s share WebView2 storage %q", other, c, want)
			}
			seen[want] = c
		})
	}

	t.Run("Given the config root cannot be resolved Then Wails falls back to its default instead of a relative path", func(t *testing.T) {
		paths.SetBuildChannelForTest(t, string(paths.ChannelBeta))
		userConfigDir = func() (string, error) { return "", errors.New("no home") }
		opts := newWailsOptions(app.NewApp(app.RuntimeModeInteractive), assets, "windows", "/tmp/x")
		if opts.Windows.WebviewUserDataPath != "" {
			t.Fatalf("WebviewUserDataPath = %q, want empty", opts.Windows.WebviewUserDataPath)
		}
	})

	t.Run("Given darwin Then no Windows options are set", func(t *testing.T) {
		opts := newWailsOptions(app.NewApp(app.RuntimeModeInteractive), assets, "darwin", "/tmp/x")
		if opts.Windows != nil {
			t.Fatalf("Windows options = %#v, want nil", opts.Windows)
		}
	})
}
