// Command tendshelf-desktop is the Tendshelf desktop window (decision 0020). It runs the library on
// this computer (personal mode) or opens a Tendshelf server (connect mode). Pages always load from
// an http(s) origin; the Wails asset handler serves only the first-run picker.
package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"runtime"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/app"
	"github.com/Tend-Stack/tendshelf/internal/desktopshell"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
)

// version is set at build time (make desktop).
var version = "dev"

const appID = "org.tendshelf.Tendshelf"

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

// windowButtons is what the page's title bar buttons do (windowControl).
type windowButtons struct{ ui *windowUI }

func (b windowButtons) Minimise()       { b.ui.win.Minimise() }
func (b windowButtons) ToggleMaximise() { b.ui.win.ToggleMaximise() }
func (b windowButtons) Close()          { b.ui.win.Close() }
func (b windowButtons) Quit()           { b.ui.app.Quit() }

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

	saved, err := desktopshell.Load(dirs.File())
	if err != nil {
		return fmt.Errorf("read the settings: %w", err)
	}
	tracker := newWindowTracker(saved.Window)
	save := func() {
		if err := saveWindow(dirs.File(), tracker.snapshot()); err != nil {
			slog.Warn("could not save the window size", "err", err)
		}
	}

	ui := &windowUI{}
	session := NewSession(dirs, version, ui)
	ui.bridge = &Bridge{origin: session.Origin}
	ui.bridge.win = windowButtons{ui}
	reveal := newRevealer(func() { go showWindow(ui, tracker) }, showFallback)
	ui.bridge.onLoad = reveal.Ready
	probe := attachProbe(ui, session)

	ui.app = application.New(application.Options{
		Name:   "Tendshelf",
		Assets: application.AssetOptions{Handler: desktopshell.New(desktopshell.Options{Starter: session, Assets: assets})},
		SingleInstance: &application.SingleInstanceOptions{
			UniqueID: appID,
			OnSecondInstanceLaunch: func(application.SecondInstanceData) {
				log.Println("second launch: bringing the window forward")
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
		OnShutdown: func() {
			save() // a Quit that never closed the window still keeps its size
			session.Stop()
		},
	})
	ui.bridge.open = ui.app.Browser.OpenURL

	actions := menuActions{
		change:    session.Change,
		quit:      func() { ui.app.Quit() },
		zoomIn:    func() { ui.win.ZoomIn() },
		zoomOut:   func() { ui.win.ZoomOut() },
		zoomReset: func() { ui.win.ZoomReset() },
	}
	// On Linux the page draws the title bar and "More" replaces the menu row (decision 0023, point 5),
	// so no menu is attached at all: HideMenuBar is an empty function there. The keys live in KeyBindings.
	menuRow := runtime.GOOS != "linux"
	if menuRow {
		menu := ui.app.Menu.New()
		buildMenu(menu, actions)
		ui.app.Menu.Set(menu)
	}

	width, height := fitWindow(saved.Window, 0, 0) // the screen is known only once the app runs
	ui.win = ui.app.Window.NewWithOptions(application.WebviewWindowOptions{
		Title:              "Tendshelf",
		Width:              width,
		Height:             height,
		MinWidth:           minWidth,
		MinHeight:          minHeight,
		BackgroundColour:   groundColour(saved.Ground),
		ZoomControlEnabled: true,
		UseApplicationMenu: menuRow,
		KeyBindings:        zoomKeys(actions),
		Hidden:             true, // shown on the first page's "ready"; see revealer
	})
	ui.bridge.exec = ui.win.ExecJS
	trackWindow(ui, tracker, save)
	setupTray(ui, assets)

	// A saved mode starts straight away; the picker shows "Opening your library…" meanwhile.
	launch := session.Begin()
	ui.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go prepareWindow(ui, saved.Window, os.Getenv("TENDSHELF_NATIVE_FRAME") == "1")
		if launch {
			go session.Launch()
		}
	})
	return ui.app.Run()
}

// trackWindow follows size, maximised and focus, tells the page about the last two, and saves the
// size when the window closes.
func trackWindow(ui *windowUI, t *windowTracker, save func()) {
	ui.win.OnWindowEvent(events.Common.WindowDidResize, func(*application.WindowEvent) {
		w, h := ui.win.Size()
		t.resized(w, h, ui.win.IsMaximised())
	})
	ui.win.OnWindowEvent(events.Common.WindowMaximise, func(*application.WindowEvent) {
		t.setMaximised(true)
		ui.bridge.Send("maximised")
	})
	ui.win.OnWindowEvent(events.Common.WindowUnMaximise, func(*application.WindowEvent) {
		t.setMaximised(false)
		ui.bridge.Send("restored")
	})
	ui.win.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) {
		t.setFocused(true)
		ui.bridge.Send("active")
	})
	ui.win.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		t.setFocused(false)
		ui.bridge.Send("inactive")
	})
	ui.win.RegisterHook(events.Common.WindowClosing, func(*application.WindowEvent) { save() })
}

// chromeReady is closed when the window's native chrome is set up (or has failed), so showing waits for it.
var chromeReady = make(chan struct{})

// prepareWindow waits for the native window, which does not exist when ApplicationStarted fires,
// then hides GTK's title bar, restores the size and releases showWindow.
func prepareWindow(ui *windowUI, saved desktopshell.Window, nativeFrame bool) {
	defer close(chromeReady)
	var handle = ui.win.NativeWindow()
	for i := 0; handle == nil && i < 100; i++ {
		time.Sleep(20 * time.Millisecond)
		handle = ui.win.NativeWindow()
	}
	application.InvokeSync(func() {
		if !nativeFrame {
			if err := hideTitlebar(handle); err != nil {
				slog.Warn("keeping the system title bar", "err", err)
			}
		}
		screen := ui.app.Screen.GetPrimary()
		if screen != nil {
			w, h := fitWindow(saved, screen.Size.Width, screen.Size.Height)
			ui.win.SetSize(w, h)
		}
		if saved.Maximised {
			ui.win.Maximise()
		}
	})
}

// showWindow shows the window after its chrome is ready, then tells the page the window's state.
func showWindow(ui *windowUI, t *windowTracker) {
	select {
	case <-chromeReady:
	case <-time.After(2 * time.Second):
		slog.Warn("showing the window before its chrome is ready")
	}
	application.InvokeSync(func() { ui.win.Show() })
	time.Sleep(300 * time.Millisecond)
	w, h := ui.win.Size()
	slog.Info("window shown", "width", w, "height", h, "maximised", ui.win.IsMaximised())
	// The page started before the window could say whether it is in front or maximised.
	if t.isFocused() {
		ui.bridge.Send("active")
	}
	if t.isMaximised() {
		ui.bridge.Send("maximised")
	}
}
