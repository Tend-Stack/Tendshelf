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
	"sync/atomic"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/app"
	"github.com/Tend-Stack/tendshelf/internal/desktopshell"
	"github.com/Tend-Stack/tendshelf/internal/web/static"
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wailsapp/wails/v3/pkg/events"
	"github.com/wailsapp/wails/v3/pkg/services/notifications"
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

	session  *Session
	dirs     desktopshell.Dirs
	version  string
	tracker  *windowTracker
	quitting atomic.Bool // a real Quit: the window must close, whatever Keep playing says
	keep     atomic.Bool // Keep playing when I close the window (saved in desktop.json)
	hasTray  bool        // a StatusNotifier watcher exists, so a hidden window can be found again
	now      *nowWatcher
}

// quit ends the app. The flag tells the closing hook that this is not a hide.
func (u *windowUI) quit() {
	u.quitting.Store(true)
	u.app.Quit()
}

// showWindow brings the window back from the tray, a minimise or a notification.
func (u *windowUI) showWindow() {
	u.win.Show()
	u.win.Restore()
	u.win.Focus()
}

// windowButtons is what the page's title bar buttons do (windowControl).
type windowButtons struct{ ui *windowUI }

func (b windowButtons) Minimise()       { b.ui.win.Minimise() }
func (b windowButtons) ToggleMaximise() { b.ui.toggleMaximise() }
func (b windowButtons) Close()          { b.ui.win.Close() }
func (b windowButtons) Quit()           { b.ui.quit() }
func (b windowButtons) ZoomIn()         { b.ui.win.ZoomIn() }
func (b windowButtons) ZoomOut()        { b.ui.win.ZoomOut() }
func (b windowButtons) ZoomReset()      { b.ui.win.ZoomReset() }
func (b windowButtons) Change()         { b.ui.session.Change() }

func (b windowButtons) About() {
	b.ui.app.Dialog.Info().SetTitle("About Tendshelf").
		SetMessage("Tendshelf " + b.ui.version + "\nYour audiobooks and ebooks, in a window of their own.").Show()
}

// KeepPlaying saves the choice. It takes effect at once; the page already shows the new state.
func (b windowButtons) KeepPlaying(on bool) {
	b.ui.keep.Store(on)
	if err := saveKeepPlaying(b.ui.dirs.File(), on); err != nil {
		slog.Warn("could not save Keep playing", "err", err)
	}
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

	ui := &windowUI{dirs: dirs, version: version, tracker: tracker}
	session := NewSession(dirs, version, ui)
	ui.session = session
	ui.bridge = &Bridge{origin: session.Origin}
	ui.bridge.win = windowButtons{ui}
	ui.hasTray = hasTrayWatcher()
	ui.keep.Store(saved.KeepPlaying && ui.hasTray)
	notes := notifications.New()
	reveal := newRevealer(func() { go showWindow(ui, tracker) }, showFallback)
	ui.bridge.onLoad = reveal.Ready
	probe := attachProbe(ui, session)

	ui.app = application.New(application.Options{
		Name: "Tendshelf",
		Icon: appIcon(assets),
		// Only Go calls this service; no page can reach Wails' runtime (TestDeskNotifyNotReachable).
		Services: []application.Service{application.NewService(notes)},
		Assets:   application.AssetOptions{Handler: desktopshell.New(desktopshell.Options{Starter: session, Assets: assets})},
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
		quit:      ui.quit,
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
		MaxWidth:           maxWidth,
		BackgroundColour:   groundColour(saved.Ground),
		ZoomControlEnabled: true,
		UseApplicationMenu: menuRow,
		KeyBindings:        zoomKeys(actions),
		Hidden:             true, // shown on the first page's "ready"; see revealer
	})
	ui.bridge.exec = ui.win.ExecJS
	trackWindow(ui, tracker, save)
	tray := setupTray(ui)
	ui.now = &nowWatcher{
		active: func() bool { return inFront(tracker.isFocused(), ui.win.IsVisible(), ui.win.IsMinimised()) },
		tray:   tray.Update,
		notify: sendNote(notes),
		ground: func(theme string) {
			if err := saveGround(dirs.File(), theme); err != nil {
				slog.Warn("could not save the theme", "err", err)
			}
		},
	}
	ui.bridge.onState = func(s nowState) {
		if s.Stop != "" {
			slog.Info("the page says playback stopped", "why", s.Stop, "focused", tracker.isFocused(), "visible", ui.win.IsVisible(), "minimised", ui.win.IsMinimised())
		}
		ui.now.Set(s)
	}
	ui.bridge.onPage = func() { // a page that has just loaded knows neither the tray nor the saved choice
		if ui.hasTray {
			ui.bridge.Send("tray")
		}
		ui.bridge.Send(map[bool]string{true: "keep", false: "nokeep"}[ui.keep.Load()])
	}
	notes.OnNotificationResponse(func(notifications.NotificationResult) { ui.showWindow() })

	// A saved mode starts straight away; the picker shows "Opening your library…" meanwhile.
	launch := session.Begin()
	ui.app.Event.OnApplicationEvent(events.Common.ApplicationStarted, func(*application.ApplicationEvent) {
		go prepareWindow(ui, tracker, saved.Window, os.Getenv("TENDSHELF_NATIVE_FRAME") == "1")
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
		if t.resized(w, h, ui.win.IsMaximised()) {
			ui.bridge.Send("restored") // an edge drag ended a fitted window
		}
		ui.limitWidth(w, h)
	})
	ui.win.OnWindowEvent(events.Common.WindowMaximise, func(*application.WindowEvent) {
		if t.isFitted() {
			return
		}
		t.setMaximised(true)
		if ui.maximiseNotFromButton() { // on a wide screen: unmaximise and fit instead; the fit tells the page
			return
		}
		ui.bridge.Send("maximised")
	})
	ui.win.OnWindowEvent(events.Common.WindowUnMaximise, func(*application.WindowEvent) {
		t.setMaximised(false)
		if !t.isFitted() {
			ui.bridge.Send("restored")
		}
	})
	ui.win.OnWindowEvent(events.Common.WindowFocus, func(*application.WindowEvent) {
		t.setFocused(true)
		ui.bridge.Send("active")
	})
	ui.win.OnWindowEvent(events.Common.WindowLostFocus, func(*application.WindowEvent) {
		t.setFocused(false)
		ui.bridge.Send("inactive")
	})
	ui.win.RegisterHook(events.Common.WindowClosing, func(e *application.WindowEvent) {
		// Keep playing: while a book plays, closing hides the window and the tray brings it back.
		if keepOnClose(ui.keep.Load(), ui.hasTray, ui.quitting.Load(), ui.now.Playing()) {
			e.Cancel()
			ui.win.Hide()
			return
		}
		save()
	})
}

// chromeReady is closed when the window's native chrome is set up (or has failed), so showing waits for it.
var chromeReady = make(chan struct{})

// prepareWindow waits for the native window, which does not exist when ApplicationStarted fires,
// then hides GTK's title bar, restores the size and releases showWindow.
func prepareWindow(ui *windowUI, tracker *windowTracker, saved desktopshell.Window, nativeFrame bool) {
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
			// Saved as maximised: on a screen wider than maxWidth that is the fit, not a full-width window.
			if x, y, w, h, ok := ui.workArea(); ok {
				if fx, fy, fw, fh, fit := fitTarget(x, y, w, h); fit {
					ui.win.SetSize(fw, fh) // still hidden: the size lands at once, nothing to wait for
					px, py := ui.win.Position()
					ui.win.SetPosition(fx, fy)
					tracker.fit(fw, fh, px, py)
					return
				}
			}
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

// appIcon is the 192 px app icon for a bare binary; the pages already ship it.
func appIcon(assets *static.Set) []byte {
	if a, ok := assets.Asset(appIconName); ok {
		return a.Body
	}
	return nil
}
