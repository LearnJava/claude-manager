package main

import (
	"embed"

	"github.com/getlantern/systray"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIconICO []byte

func main() {
	// Create an instance of the app structure
	app := NewApp()

	// systray.Run blocks its own goroutine for the process lifetime; it must
	// start before wails.Run (which blocks main) and is torn down from
	// app.shutdown via systray.Quit().
	go systray.Run(app.onTrayReady, func() {})

	// Create application with options
	err := wails.Run(&options.App{
		Title:           "Claude Session Manager",
		Width:           1280,
		Height:          800,
		WindowStartState: options.Maximised,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour: &options.RGBA{R: 18, G: 18, B: 18, A: 1},
		// Closing the window hides it instead of quitting — pairs with the
		// tray icon / ShowMainWindow binding for minimise-to-tray behaviour.
		HideWindowOnClose: true,
		OnStartup:         app.startup,
		OnShutdown:        app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
