// Command tendshelf-desktop is the Tendshelf desktop window (decision 0020). It runs the library on
// this computer (personal mode) or opens a Tendshelf server (connect mode). Pages always load from
// an http(s) origin; the Wails asset handler serves only the first-run picker.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/Tend-Stack/tendshelf/internal/app"
	"github.com/Tend-Stack/tendshelf/internal/desktopshell"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// version is set at build time (make desktop).
var version = "dev"

const (
	appID = "org.tendshelf.Tendshelf"

	// darkGround is --ground of the dark theme, so the window never flashes white.
	darkR, darkG, darkB = 0x14, 0x11, 0x0e
)

func main() {
	dirs, err := desktopshell.Paths()
	if err != nil {
		log.Fatal(err)
	}
	if len(os.Args) > 1 && os.Args[1] == "reset-password" {
		args := append([]string{"-data", dirs.Library}, os.Args[2:]...)
		if err := app.ResetPassword(context.Background(), args, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}
	if err := run(dirs); err != nil {
		log.Fatal(err)
	}
}

// windowUI is the session's view of the window. The window exists only after the application, and
// the application needs the session's handler, so the fields are filled in afterwards.
type windowUI struct {
	app    *application.App
	win    *application.WebviewWindow
	bridge *Bridge
}

func (u *windowUI) Load(url string) {
	u.bridge.Reset()
	u.win.SetURL(url)
}

func (u *windowUI) Warn(message string) {
	u.app.Dialog.Warning().SetTitle("Tendshelf").SetMessage(message).Show()
}

func run(dirs desktopshell.Dirs) error {
	// Before application.New: the cookie store and the download hook go on WebKit's default session.
	if err := initWebKit(dirs.Cookies()); err != nil {
		return err
	}
	assets, err := static.Load()
	if err != nil {
		return fmt.Errorf("load the pages: %w", err)
	}

	ui := &windowUI{}
	session := NewSession(dirs, version, ui)
	ui.bridge = &Bridge{origin: session.Origin}
	probe := attachProbe(ui, session)

	ui.app = application.New(application.Options{
		Name:   "Tendshelf",
		Assets: application.AssetOptions{Handler: desktopshell.New(desktopshell.Options{Starter: session, Assets: assets})},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: appID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				if ui.win != nil {
					ui.win.Restore()
					ui.win.Focus()
				}
			},
		},
		Linux: application.LinuxOptions{ProgramName: "tendshelf-desktop", ApplicationID: appID},
		RawMessageHandler: func(_ application.Window, message string, from *application.OriginInfo) {
			uri := ""
			if from != nil {
				uri = from.Origin
			}
			if probe != nil && probe(message) {
				return
			}
			ui.bridge.Handle(message, uri)
		},
		OnShutdown: session.Stop,
	})
	ui.bridge.open = ui.app.Browser.OpenURL

	actions := menuActions{
		change:    session.Change,
		quit:      func() { ui.app.Quit() },
		zoomIn:    func() { ui.win.ZoomIn() },
		zoomOut:   func() { ui.win.ZoomOut() },
		zoomReset: func() { ui.win.ZoomReset() },
	}
	menu := ui.app.Menu.New()
	buildMenu(menu, actions)
	ui.app.Menu.Set(menu)

	ui.win = ui.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:              "Tendshelf",
		Width:              1280,
		Height:             820,
		MinWidth:           390,
		MinHeight:          600,
		BackgroundColour:   application.NewRGB(darkR, darkG, darkB),
		ZoomControlEnabled: true,
		UseApplicationMenu: true,
		KeyBindings:        zoomKeys(actions),
	})
	ui.bridge.exec = ui.win.ExecJS
	setupTray(ui, assets)

	// A saved mode starts straight away; the picker shows "Opening your library…" meanwhile.
	if session.Begin() {
		ui.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
			go session.Launch()
		})
	}
	return ui.app.Run()
}
