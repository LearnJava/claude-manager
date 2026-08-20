package main

import (
	"embed"
	"runtime"

	"claude-manager/internal/logger"

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

	// getlantern/systray on Linux runs its own gtk_main() loop (systray_linux.c:
	// registerSystray calls gtk_init, nativeLoop calls gtk_main) on a separate
	// OS thread — a second GTK main loop that collides with Wails' own
	// webkit2gtk frontend loop and segfaults the process on startup. The tray
	// is a Windows-only feature here (Shell_NotifyIcon, no GTK involved), so
	// it only runs on Windows; HideWindowOnClose follows it so a close on
	// other platforms quits normally instead of hiding a window with no tray
	// left to bring it back from.
	app.trayEnabled = runtime.GOOS == "windows"

	if app.trayEnabled {
		// systray.Run blocks its own goroutine for the process lifetime; it
		// must start before wails.Run (which blocks main) and is torn down
		// from app.shutdown via systray.Quit().
		go func() {
			defer logger.Recover("systray.run")
			systray.Run(app.onTrayReady, func() {})
		}()
	}

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
		// Only makes sense where the tray actually exists (see above).
		HideWindowOnClose: app.trayEnabled,
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
