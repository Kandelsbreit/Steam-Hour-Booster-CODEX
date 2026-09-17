package main

import (
	"crypto/sha256"
	"embed"
	"fmt"
	"github.com/Kandelsbreit/Steam-Hour-Booster-CODEX/internal/platform"
	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:frontend
var assets embed.FS

func main() {
	profile := ""
	for _, arg := range os.Args[1:] {
		if strings.HasPrefix(arg, "--test-profile=") {
			profile = strings.TrimPrefix(arg, "--test-profile=")
		}
	}
	dir, err := platform.DataDir()
	if profile != "" {
		dir, err = filepath.Abs(profile)
	}
	if err != nil {
		platform.ErrorBox("Не удалось открыть папку данных Windows")
		return
	}
	app := &App{dir: dir, test: profile != ""}
	key := fmt.Sprintf("steamhours-wails-%x", sha256.Sum256([]byte(strings.ToLower(dir))))
	err = wails.Run(&options.App{Title: "Steam Hours Booster", Width: 1260, Height: 900, MinWidth: 980, MinHeight: 700, BackgroundColour: options.NewRGB(16, 18, 27), AssetServer: &assetserver.Options{Assets: assets}, Bind: []interface{}{app}, OnStartup: app.startup, OnDomReady: app.ready, OnShutdown: app.shutdown, OnBeforeClose: app.beforeClose, SingleInstanceLock: &options.SingleInstanceLock{UniqueId: key, OnSecondInstanceLaunch: func(options.SecondInstanceData) { app.show() }}, Windows: &windows.Options{Theme: windows.Dark, WebviewUserDataPath: filepath.Join(dir, "webview2"), OnSuspend: func() { app.suspend() }, OnResume: func() { app.suspend() }}})
	if err != nil {
		platform.ErrorBox("Не удалось запустить приложение: " + err.Error())
	}
}
