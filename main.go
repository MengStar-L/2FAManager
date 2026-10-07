package main

import (
	"LumaAuthenticator/internal/updateinstall"
	"embed"
	"os"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	if handled, err := updateinstall.HandleHelper(os.Args[1:]); handled {
		if err != nil {
			os.Exit(1)
		}
		return
	}
	// Create an instance of the app structure
	app := NewApp()

	// Create application with options
	err := wails.Run(&options.App{
		Title:     "Luma · 拾光验证器",
		Width:     1180,
		Height:    800,
		MinWidth:  760,
		MinHeight: 560,
		Frameless: true,
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: singleInstanceID(),
			OnSecondInstanceLaunch: func(_ options.SecondInstanceData) {
				app.showMainWindow()
			},
		},
		AssetServer: &assetserver.Options{
			Assets:  assets,
			Handler: app.mediaHandler(),
		},
		BackgroundColour: &options.RGBA{R: 251, G: 248, B: 241, A: 255},
		Windows:          &windows.Options{Theme: windows.Light, DisableFramelessWindowDecorations: true, DisableWindowIcon: false, WindowClassName: mainWindowClass},
		OnStartup:        app.startup,
		OnDomReady:       app.domReady,
		OnBeforeClose:    app.beforeClose,
		OnShutdown:       app.shutdown,
		Bind: []interface{}{
			app,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
