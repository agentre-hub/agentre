// Package desktop owns the production-neutral Wails desktop lifecycle shared by
// the production and dedicated E2E entrypoints. It must not import e2e packages.
package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"runtime"

	"github.com/agentre-ai/agentre/internal/app"
	"github.com/agentre-ai/agentre/internal/bootstrap"
	"github.com/agentre-ai/agentre/internal/pkg/paths"

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

// NewOptions exposes the Wails options seam for focused entrypoint tests.
func NewOptions(opts Options) *options.App {
	appInst := opts.App
	if appInst == nil {
		appInst = app.NewApp(opts.RuntimeMode)
	}
	return newWailsOptions(appInst, opts.Assets, opts.GOOS, opts.DataDir)
}

func newWailsOptions(a *app.App, assets fs.FS, goos, dataDir string) *options.App {
	appOptions := &options.App{
		Title:            windowTitle(),
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
				logger.Default().Info("desktop: second instance launch",
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

	configurePlatformWindowOptions(appOptions, goos)
	return appOptions
}

func windowTitle() string {
	if paths.IsDevMode() {
		return "Agentre (Dev)"
	}
	return "Agentre"
}

func singleInstanceUniqueID(dataDir string) string {
	sum := sha256.Sum256([]byte(dataDir))
	return "agentre-" + hex.EncodeToString(sum[:8])
}

func configurePlatformWindowOptions(appOptions *options.App, goos string) {
	if goos != "windows" {
		return
	}
	appOptions.Frameless = true
	appOptions.Windows = &windows.Options{
		Theme:                             windows.SystemDefault,
		DisableFramelessWindowDecorations: false,
	}
}
