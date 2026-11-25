package main

import (
	"embed"
	"log"
	"os"
	"path/filepath"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"dsxid/internal/app"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	application, err := app.NewApp()
	if err != nil {
		log.Fatal(err)
	}

	// In a GUI build os.Stderr has no console, so write the log to a file.
	logPath := filepath.Join(application.DataDir(), "dsxid.log")
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		defer f.Close()
		log.SetOutput(f)
		log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	}
	log.Printf("DsxID %s starting, data dir: %s", app.Version, application.DataDir())

	err = wails.Run(&options.App{
		Title:             "DsxID",
		Width:             1120,
		Height:            760,
		DisableResize:     true,
		HideWindowOnClose: true,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "DsxID-local-identity",
			OnSecondInstanceLaunch: func(options.SecondInstanceData) {
				application.ShowWindow()
			},
		},
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 243, G: 243, B: 243, A: 1},
		OnStartup:        application.Startup,
		OnShutdown:       application.Shutdown,
		Bind: []interface{}{
			application,
		},
		Windows: &windows.Options{
			Theme:                windows.Light,
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			CustomTheme: &windows.ThemeSettings{
				DarkModeTitleBar:           windows.RGB(243, 243, 243),
				DarkModeTitleBarInactive:   windows.RGB(243, 243, 243),
				DarkModeTitleText:          windows.RGB(27, 27, 27),
				DarkModeTitleTextInactive:  windows.RGB(109, 109, 109),
				DarkModeBorder:             windows.RGB(224, 224, 224),
				DarkModeBorderInactive:     windows.RGB(236, 236, 236),
				LightModeTitleBar:          windows.RGB(243, 243, 243),
				LightModeTitleBarInactive:  windows.RGB(243, 243, 243),
				LightModeTitleText:         windows.RGB(27, 27, 27),
				LightModeTitleTextInactive: windows.RGB(109, 109, 109),
				LightModeBorder:            windows.RGB(224, 224, 224),
				LightModeBorderInactive:    windows.RGB(236, 236, 236),
			},
		},
	})
	if err != nil {
		log.Fatalf("DsxID failed: %v", err)
	}
	log.Printf("DsxID stopped")
}
