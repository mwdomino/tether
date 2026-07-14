//go:build darwin

package main

import (
	"context"
	"fmt"
	"os"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/mwdomino/tether/internal/autostart"
	"github.com/mwdomino/tether/internal/config"
	"github.com/mwdomino/tether/internal/registry"
	"github.com/mwdomino/tether/internal/uistate"
)

const appID = "io.github.mwdomino.tether"

func main() {
	sock, err := resolveControlSocket()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tether-gui:", err)
		os.Exit(1)
	}

	// Declare app identity (needed by the Preferences API) and opt into the
	// fyne.Do threading model — all cross-goroutine UI updates go through
	// fyne.Do, so this silences the migration banner.
	fyneapp.SetMetadata(fyne.AppMetadata{
		ID:         appID,
		Name:       "Tether",
		Migrations: map[string]bool{"fyneDo": true},
	})

	a := fyneapp.NewWithID(appID)
	a.SetIcon(appIcon())

	w := a.NewWindow("Tether")
	w.Resize(fyne.NewSize(560, 440))
	w.SetCloseIntercept(func() { w.Hide() }) // closing hides to the menubar

	exe, _ := os.Executable()
	ui := &gui{
		app:   a,
		win:   w,
		prefs: a.Preferences(),
		agent: autostart.Agent{Label: appID + ".login", Program: []string{exe}},
	}
	ui.desk, ui.isDesk = a.(desktop.App)
	ui.build()

	if ui.isDesk {
		ui.desk.SetSystemTrayIcon(iconFor(uistate.HealthDaemonDown))
		ui.desk.SetSystemTrayMenu(ui.trayMenu(uistate.View{}))
	}

	model := uistate.NewModel(uistate.Options{
		Addr:     sock,
		OnUpdate: func(v uistate.View) { fyne.Do(func() { ui.update(v) }) },
	})
	ui.model = model

	ctx, cancel := context.WithCancel(context.Background())
	a.Lifecycle().SetOnStarted(func() {
		ui.reconcileAutostart()
		go model.Run(ctx)
	})
	a.Lifecycle().SetOnStopped(cancel)

	a.Run()
}

func resolveControlSocket() (string, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return "", err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return "", err
	}
	return cfg.ControlSocketPath()
}

type gui struct {
	app    fyne.App
	win    fyne.Window
	model  *uistate.Model
	desk   desktop.App
	isDesk bool
	prefs  fyne.Preferences
	agent  autostart.Agent

	healthLabel *widget.Label
	boxesBox    *fyne.Container
	reqList     *widget.List
	reqs        []registry.RequestRecord
	lastView    uistate.View
}

func (ui *gui) build() {
	ui.healthLabel = widget.NewLabel("Connecting…")
	ui.healthLabel.TextStyle = fyne.TextStyle{Bold: true}
	ui.boxesBox = container.NewVBox()
	ui.reqList = widget.NewList(
		func() int { return len(ui.reqs) },
		func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) {
			if i < 0 || i >= len(ui.reqs) {
				return
			}
			r := ui.reqs[i]
			o.(*widget.Label).SetText(fmt.Sprintf("%s  [%s]  %s  (%s)",
				r.Time.Format("15:04:05"), r.Box, r.URL, r.Outcome))
		},
	)
	reload := widget.NewButton("Reload", func() {
		if ui.model != nil {
			_ = ui.model.Reload()
		}
	})

	header := container.NewVBox(
		ui.healthLabel,
		ui.boxesBox,
		widget.NewSeparator(),
		widget.NewLabel("Recent requests"),
	)
	ui.win.SetContent(container.NewBorder(header, reload, nil, nil, ui.reqList))
}

func (ui *gui) update(v uistate.View) {
	ui.lastView = v
	h := uistate.Aggregate(v)
	ui.healthLabel.SetText("Status: " + h.String())

	ui.boxesBox.Objects = nil
	for _, b := range v.Boxes {
		line := fmt.Sprintf("%s  %s  (%s)", glyph(b.State), b.Name, b.State)
		if b.LastError != "" {
			line += " — " + b.LastError
		}
		ui.boxesBox.Add(widget.NewLabel(line))
	}
	ui.boxesBox.Refresh()

	ui.reqs = reverse(v.Requests) // most recent first
	ui.reqList.Refresh()

	ui.refreshTray()
}

func (ui *gui) refreshTray() {
	if ui.isDesk {
		ui.desk.SetSystemTrayIcon(iconFor(uistate.Aggregate(ui.lastView)))
		ui.desk.SetSystemTrayMenu(ui.trayMenu(ui.lastView))
	}
}

func (ui *gui) trayMenu(v uistate.View) *fyne.Menu {
	head := fyne.NewMenuItem(uistate.Aggregate(v).String(), nil)
	head.Disabled = true
	items := []*fyne.MenuItem{head, fyne.NewMenuItemSeparator()}

	for _, b := range v.Boxes {
		it := fyne.NewMenuItem(fmt.Sprintf("%s  %s (%s)", glyph(b.State), b.Name, b.State), nil)
		it.Disabled = true
		items = append(items, it)
	}

	login := fyne.NewMenuItem("Start at login", ui.toggleAutostart)
	login.Checked = ui.autostartWanted()

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Open Tether…", func() { ui.win.Show(); ui.win.RequestFocus() }),
		fyne.NewMenuItem("Reload", func() {
			if ui.model != nil {
				_ = ui.model.Reload()
			}
		}),
		login,
		fyne.NewMenuItem("Quit", func() { ui.app.Quit() }),
	)
	return fyne.NewMenu("tether", items...)
}

func (ui *gui) autostartWanted() bool {
	return ui.prefs.BoolWithFallback("autostart", true)
}

// reconcileAutostart makes the on-disk login item match the saved preference
// (default on), so a fresh install sets itself up to launch at login.
func (ui *gui) reconcileAutostart() {
	want := ui.autostartWanted()
	enabled, _ := ui.agent.Enabled()
	switch {
	case want && !enabled:
		_ = ui.agent.Enable()
	case !want && enabled:
		_ = ui.agent.Disable()
	}
}

func (ui *gui) toggleAutostart() {
	want := !ui.autostartWanted()
	ui.prefs.SetBool("autostart", want)
	if want {
		_ = ui.agent.Enable()
	} else {
		_ = ui.agent.Disable()
	}
	ui.refreshTray()
}

func glyph(state string) string {
	switch state {
	case "connected":
		return "●"
	case "connecting":
		return "◐"
	default:
		return "○"
	}
}

// appIcon is the fixed app/window icon (the rope knot on a teal square).
func appIcon() fyne.Resource {
	return fyne.NewStaticResource("tether.png", appIconPNG)
}

// iconFor returns the status-tinted rope knot for the menubar.
func iconFor(h uistate.Health) fyne.Resource {
	var png []byte
	switch h {
	case uistate.HealthOK:
		png = trayGreenPNG
	case uistate.HealthDegraded:
		png = trayAmberPNG
	case uistate.HealthDown:
		png = trayRedPNG
	default:
		png = trayGreyPNG // empty / daemon down
	}
	return fyne.NewStaticResource("tether-status.png", png)
}

func reverse(in []registry.RequestRecord) []registry.RequestRecord {
	out := make([]registry.RequestRecord, len(in))
	for i, r := range in {
		out[len(in)-1-i] = r
	}
	return out
}
