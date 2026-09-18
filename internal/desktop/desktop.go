// Package desktop owns the production-neutral Wails desktop lifecycle shared by
// the production and dedicated E2E entrypoints. It must not import e2e packages.
package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/agentre-hub/agentre/internal/app"
	"github.com/agentre-hub/agentre/internal/bootstrap"
	"github.com/agentre-hub/agentre/internal/pkg/paths"

	"github.com/cago-frame/cago/pkg/logger"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"go.uber.org/zap"
)

const (
	defaultWindowWidth  = 1024
	defaultWindowHeight = 768
	minWindowWidth      = 860
	minWindowHeight     = 640
)

// Options are supplied explicitly by an executable composition root.
type Options struct {
	App            *app.App
	Assets         fs.FS
	DataDir        string
	GOOS           string
	RuntimeMode    app.RuntimeMode
	AfterBootstrap func(context.Context, *bootstrap.Runtime) error
}

var (
	bootstrapDesktop = bootstrap.Init
	runWails         = wails.Run
	userConfigDir    = os.UserConfigDir
)

// Run initializes persistent production services, optionally installs entrypoint
// composition, then starts Wails. Callers that need preflight must run it before
// invoking Run, because bootstrap creates logs and opens the database.
func Run(ctx context.Context, opts Options) error {
	boot, err := bootstrapDesktop(ctx)
	if err != nil {
		return fmt.Errorf("init cago: %w", err)
	}
	defer boot.Close()

	if opts.AfterBootstrap != nil {
		if err := opts.AfterBootstrap(ctx, boot); err != nil {
			return fmt.Errorf("install desktop composition: %w", err)
		}
	}

	goos := opts.GOOS
	if goos == "" {
		goos = runtime.GOOS
	}
	appInst := opts.App
	if appInst == nil {
		appInst = app.NewApp(opts.RuntimeMode)
	}
	if err := runWails(newWailsOptions(appInst, opts.Assets, goos, boot.DataDir())); err != nil {
		logger.Default().Error("desktop.Run: wails run failed", zap.Error(err))
		return fmt.Errorf("wails run: %w", err)
	}
	return nil
}

func newWailsOptions(a *app.App, assets fs.FS, goos, dataDir string) *options.App {
	// bootstrap.Init 已经在渠道标记非法时拒绝启动，走到这里渠道必然合法。
	channel, _ := paths.CurrentChannel()

	appOptions := &options.App{
		Title:            channel.Identity().DisplayName,
		Width:            defaultWindowWidth,
		Height:           defaultWindowHeight,
		MinWidth:         minWindowWidth,
		MinHeight:        minWindowHeight,
		StartHidden:      true,
		AssetServer:      &assetserver.Options{Assets: assets},
		BackgroundColour: &options.RGBA{R: 27, G: 38, B: 54, A: 1},
		OnStartup:        a.Startup,
		OnShutdown:       a.Shutdown,
		OnBeforeClose:    a.OnBeforeClose,
		Mac: &mac.Options{
			TitleBar: mac.TitleBarHiddenInset(),
		},
		Bind:        []interface{}{a},
		DragAndDrop: &options.DragAndDrop{EnableFileDrop: true},
	}

	if !paths.IsDevMode() {
		appOptions.SingleInstanceLock = &options.SingleInstanceLock{
			UniqueId: singleInstanceUniqueID(dataDir),
			OnSecondInstanceLaunch: func(secondInstanceData options.SecondInstanceData) {
				logger.Default().Info("desktop.Run: second instance launch",
					zap.Strings("args", secondInstanceData.Args),
					zap.String("workingDirectory", secondInstanceData.WorkingDirectory))
			},
		}
	} else {
		// Wails dev only: the asset proxy dials vite and can hit a burst of
		// `connection reset by peer` on first load (upstream wails#4556, unfixed
		// in v2). Retry GET 5xx so the webview never lands on a black shell.
		appOptions.AssetServer.Middleware = devProxyRetryMiddleware
	}

	configurePlatformWindowOptions(appOptions, goos, channel.Identity())
	return appOptions
}

func singleInstanceUniqueID(dataDir string) string {
	sum := sha256.Sum256([]byte(dataDir))
	return "agentre-" + hex.EncodeToString(sum[:8])
}

func configurePlatformWindowOptions(appOptions *options.App, goos string, id paths.Identity) {
	if goos != "windows" {
		return
	}
	appOptions.Frameless = true
	appOptions.Windows = &windows.Options{
		Theme:                             windows.SystemDefault,
		DisableFramelessWindowDecorations: false,
		WebviewUserDataPath:               webviewUserDataPath(id),
	}
}

// webviewUserDataPath keeps each channel's WebView2 storage apart. Every channel's
// executable is Agentre.exe, so Wails' default (%AppData%\<exe name>) would be shared.
// It follows the channel identity, not AGENTRE_DATA_DIR: an override only moves the
// data dir. An unresolvable config root leaves the Wails default rather than a
// relative path.
func webviewUserDataPath(id paths.Identity) string {
	root, err := userConfigDir()
	if err != nil || root == "" {
		logger.Default().Warn("desktop.webviewUserDataPath: user config dir unavailable, using WebView2 default", zap.Error(err))
		return ""
	}
	return filepath.Join(root, id.DataDirName, "WebView2")
}
