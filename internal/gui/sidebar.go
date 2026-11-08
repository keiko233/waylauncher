package gui

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

func (a *App) navRow(key string, icon *ui.SVG, iconColor ui.Color, label string, on, disabled bool, trail func()) bool {
	b := ui.ButtonBase(a.c.Key(key)).Height(32).Padding(0, 10).Gap(9).Radius(9).AlignItems(ui.Center).
		Label(label).Disabled(disabled)
	switch {
	case on:
		b.Background(a.p.navOn)
	case !disabled && b.Hovered():
		b.Background(a.p.hover)
	}
	b.Children(func() {
		ui.Icon(a.c, icon).FontSize(16).TextColor(iconColor).Shrink(0)
		t := ui.Text(a.c, label).Grow(1).SingleLine().FontSize(13)
		if on {
			t.TextColor(a.p.navOnText).FontWeight(600)
		}
		if disabled {
			t.TextColor(a.p.text)
		}
		if trail != nil {
			trail()
		}
	})
	if disabled {
		b.Opacity(0.55)
	}
	return !disabled && b.Clicked()
}

func (a *App) sidebar() {
	c, p, st := a.c, a.p, &a.st
	ui.Box(c).Height(titleBarHeight).Shrink(0).DragWindow()
	if ui.SearchField(c, &a.query).Label("Search applications").Placeholder("Search").Changed() {
		a.page = PageApps
	}
	ui.Box(c).Height(8)

	a.sectionLabel("Library")
	if a.navRow("nav-apps", icApps, p.icApps, "Applications", a.page == PageApps, false, func() {
		a.muted(itoa(len(st.Apps)), 12)
	}) {
		a.page = PageApps
	}
	if a.navRow("nav-running", icRun, p.icRun, "Running", a.page == PageRunning, false, func() {
		if st.Live > 0 {
			ui.Badge(c, itoa(st.Live)).Background(p.accent).TextColor(p.accentText)
		}
	}) {
		a.page = PageRunning
	}
	a.navRow("nav-discover", icDisc, p.icDisc, "Discover", false, true, func() {
		a.muted("Coming soon", 12)
	})

	ui.Row(c).AlignItems(ui.Center).Children(func() {
		a.sectionLabel("Hosts")
		ui.Spacer(c)
		add := ui.ButtonBase(c.Key("add-host")).Size(20, 20).Radius(6).Center().Label("Add host").Tooltip("Add host").TextColor(p.muted)
		if add.Hovered() {
			add.Background(p.hover)
		}
		add.Children(func() { ui.Icon(c, icPlus).FontSize(12) })
		if add.Clicked() {
			a.openHost("")
		}
	})
	if len(st.Hosts) == 0 {
		a.muted("No hosts yet", 12).Padding(4, 10)
	}
	for _, h := range st.Hosts {
		label := ""
		dot := p.idle.fg
		switch {
		case !h.Checked:
		case h.Reachable:
			label, dot = "reachable", p.ok.fg
		default:
			label, dot = "unreachable", p.err.fg
		}
		h := h
		if a.navRow("host-"+h.ID, icHost, dot, h.ID, a.page == PageHost && a.hostID == h.ID, false, func() {
			if label != "" {
				a.muted(label, 12)
			}
		}) {
			a.page, a.hostID = PageHost, h.ID
		}
	}

	ui.Spacer(c)
	if a.navRow("nav-settings", icSet, p.icSet, "Settings", a.page == PageSettings, false, nil) {
		a.page = PageSettings
	}
	a.cocoaCard()
}

func (a *App) cocoaCard() {
	c, p, co := a.c, a.p, a.st.Cocoa
	title, sub, dot := "Cocoa-Way not running", "Starts when you open an app", p.warn.fg
	if co.Reachable {
		title, sub, dot = "Cocoa-Way ready", "Control socket · protocol "+itoa(co.Protocol), p.ok.fg
	}
	// The card is a pane of Liquid Glass (glass plugin); with Reduce
	// Transparency the plugin draws an opaque surface instead.
	card := ui.Row(c).Padding(10).Gap(10).Radius(12).Margin(6, 0, 0).AlignItems(ui.Center)
	card.Material(glass.Glass{})
	card.Children(func() {
		a.cocoaMark(20, dot)
		ui.Column(c).MinWidth(0).Children(func() {
			ui.Text(c, title).FontSize(12).FontWeight(600).SingleLine()
			a.muted(sub, 12).SingleLine()
		})
	})
}
