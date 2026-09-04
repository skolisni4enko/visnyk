package main

import (
	"context"
	"embed"
	"log"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"

	"visnyk/internal/paths"
	"visnyk/internal/ui"
)

// Version is set via ldflags -X main.Version=0.1.0 or wails.json info.productVersion
var Version = "0.1.0"

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	// Migrate legacy ./telegram-store and ./whatsapp-store into UserConfigDir on first installed run
	_ = paths.MigrateLegacy()
	_ = paths.EnsureDataDirs()

	app, err := ui.NewApp("")
	if err != nil {
		log.Fatalf("create app: %v", err)
	}

	err = wails.Run(&options.App{
		Title:         "Visnyk v" + Version,
		Width:         960,
		Height:        700,
		MinWidth:      960,
		MaxWidth:      960,
		MinHeight:     700,
		MaxHeight:     700,
		DisableResize: true,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 246, G: 247, B: 249, A: 1},
		OnStartup:        app.Startup,
		OnShutdown: func(ctx context.Context) {
			app.Shutdown(ctx)
		},
		Bind: []interface{}{
			app,
		},
	})
	if err != nil {
		log.Fatal(err)
	}
}
