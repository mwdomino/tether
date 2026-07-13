//go:build darwin

package main

import (
	"context"
	"fmt"
	"image/color"
	"os"

	"fyne.io/fyne/v2"
	fyneapp "fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/driver/desktop"
	"fyne.io/fyne/v2/widget"

	"github.com/mwdomino/tether/internal/config"
	"github.com/mwdomino/tether/internal/registry"
	"github.com/mwdomino/tether/internal/uistate"
)

func main() {
	sock, err := resolveControlSocket()
	if err != nil {
		fmt.Fprintln(os.Stderr, "tether-gui:", err)
		os.Exit(1)
	}

	a := fyneapp.New()
	a.SetIcon(iconFor(uistate.HealthDaemonDown))

	w := a.NewWindow("Tether")
	w.Resize(fyne.NewSize(560, 440))
	w.SetCloseIntercept(func() { w.Hide() }) // closing the window hides to the menubar

	ui := &gui{app: a, win: w}
	ui.build()

	desk, isDesk := a.(desktop.App)
	if isDesk {
		desk.SetSystemTrayIcon(iconFor(uistate.HealthDaemonDown))
		desk.SetSystemTrayMenu(ui.trayMenu(uistate.View{}))
	}

	model := uistate.NewModel(uistate.Options{
		Addr: sock,
		OnUpdate: func(v uistate.View) {
			fyne.Do(func() { ui.update(v, desk, isDesk) })
		},
	})
	ui.model = model

	ctx, cancel := context.WithCancel(context.Background())
	a.Lifecycle().SetOnStarted(func() { go model.Run(ctx) })
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
	app   fyne.App
	win   fyne.Window
	model *uistate.Model

	healthLabel *widget.Label
	boxesBox    *fyne.Container
	reqList     *widget.List
	reqs        []registry.RequestRecord
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

func (ui *gui) update(v uistate.View, desk desktop.App, isDesk bool) {
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

	icon := iconFor(h)
	ui.app.SetIcon(icon)
	if isDesk {
		desk.SetSystemTrayIcon(icon)
		desk.SetSystemTrayMenu(ui.trayMenu(v))
	}
}

func (ui *gui) trayMenu(v uistate.View) *fyne.Menu {
	head := fyne.NewMenuItem(uistate.Aggregate(v).String(), nil)
	head.Disabled = true
	items := []*fyne.MenuItem{head, fyne.NewMenuItemSeparator()}

	for _, b := range v.Boxes {
		label := fmt.Sprintf("%s  %s (%s)", glyph(b.State), b.Name, b.State)
		it := fyne.NewMenuItem(label, nil)
		it.Disabled = true
		items = append(items, it)
	}

	items = append(items,
		fyne.NewMenuItemSeparator(),
		fyne.NewMenuItem("Open Tether…", func() { ui.win.Show(); ui.win.RequestFocus() }),
		fyne.NewMenuItem("Reload", func() {
			if ui.model != nil {
				_ = ui.model.Reload()
			}
		}),
		fyne.NewMenuItem("Quit", func() { ui.app.Quit() }),
	)
	return fyne.NewMenu("tether", items...)
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

func iconFor(h uistate.Health) fyne.Resource {
	var c color.Color
	switch h {
	case uistate.HealthOK:
		c = color.RGBA{0x3c, 0xb3, 0x71, 0xff} // green
	case uistate.HealthDegraded:
		c = color.RGBA{0xe6, 0xa0, 0x23, 0xff} // amber
	case uistate.HealthDown:
		c = color.RGBA{0xd0, 0x3a, 0x3a, 0xff} // red
	default:
		c = color.RGBA{0x9e, 0x9e, 0x9e, 0xff} // grey (empty / daemon down)
	}
	return fyne.NewStaticResource("tether-status.png", statusDotPNG(c))
}

func reverse(in []registry.RequestRecord) []registry.RequestRecord {
	out := make([]registry.RequestRecord, len(in))
	for i, r := range in {
		out[len(in)-1-i] = r
	}
	return out
}
