package gui

import (
	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/launcher"
)

type btnKind int

const (
	btnPlain btnKind = iota
	btnPrimary
	btnDangerText
)

// button is a capsule button, 28 high (32 when large).
func (a *App) button(label string, kind btnKind) ui.Element {
	var b ui.Element
	if kind == btnPrimary {
		b = ui.PrimaryButton(a.c, label)
	} else {
		b = ui.Button(a.c, label)
	}
	b.Height(28).Padding(0, 13).Radius(14).FontSize(12.5).FontWeight(500)
	if kind == btnDangerText {
		b.TextColor(a.p.dangerText)
	}
	return b
}

func (a *App) keyedButton(key, label string, kind btnKind) ui.Element {
	a.c.Key(key)
	return a.button(label, kind)
}

// iconButton is a primary capsule button with a leading icon.
func (a *App) iconButton(icon *ui.SVG, label string, kind btnKind) ui.Element {
	var b ui.Element
	if kind == btnPrimary {
		b = ui.PrimaryButton(a.c, "")
	} else {
		b = ui.Button(a.c, "")
	}
	b.Label(label).Height(28).Padding(0, 13).Radius(14).Gap(6).FontSize(12.5).FontWeight(500)
	b.Children(func() {
		ui.Icon(a.c, icon).FontSize(12)
		ui.Text(a.c, label).SingleLine()
	})
	return b
}

// pill is a status chip: a dot and a label on a tinted capsule.
func (a *App) pill(kind launcher.Kind, label string, small bool) {
	k := a.p.tone(kind)
	h := float32(24)
	if small {
		h = 22
	}
	ui.Row(a.c).Height(h).Padding(0, 9).Gap(6).Radius(h / 2).Background(k.bg).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Box(a.c).Size(6, 6).Radius(3).Background(k.fg).Shrink(0)
		ui.Text(a.c, label).FontSize(12).FontWeight(600).TextColor(k.fg).SingleLine()
	})
}

func (a *App) chip(label string) {
	ui.Box(a.c).Padding(2, 8).Radius(6).Background(a.p.chip).Children(func() {
		ui.Text(a.c, label).FontSize(11.5).TextColor(a.p.chipText).SingleLine()
	})
}

// tile is an application's coloured square with its glyph.
func (a *App) tile(glyph, color string, size, radius, fs float32) {
	ui.Box(a.c).Size(size, size).Radius(radius).Shrink(0).Center().Background(ui.Hex(color)).Children(func() {
		ui.Text(a.c, glyph).Font("monospace").FontSize(fs).FontWeight(600).TextColor(ui.Hex("#FFFFFF")).SingleLine()
	})
}

func (a *App) muted(s string, size float32) ui.Element {
	return ui.Text(a.c, s).FontSize(size).TextColor(a.p.muted)
}

func (a *App) mono(s string, size float32) ui.Element {
	return ui.Text(a.c, s).Font("monospace").FontSize(size)
}

// panelBox is a content container: fill on the window background, no stroke.
func (a *App) panelBox(e ui.Element) ui.Element {
	return e.Radius(12).Background(a.p.panel)
}

func (a *App) sectionLabel(s string) {
	ui.Text(a.c, s).FontSize(11).FontWeight(600).TextColor(a.p.muted).Padding(14, 10, 4)
}

// header is the unified toolbar title: title and subtitle on the left,
// controls on the right, draggable.
func (a *App) header(title, sub string, controls func()) {
	bar := a.c.TitleBar()
	ui.Row(a.c).Height(titleBarHeight).Shrink(0).AlignItems(ui.Center).Gap(10).
		Padding(0, 16+bar.Right, 0, 24).DragWindow().Children(func() {
		ui.Column(a.c).Gap(2).MinWidth(0).Shrink(1).Children(func() {
			ui.Text(a.c, title).FontSize(17).Bold().SingleLine()
			if sub != "" {
				a.muted(sub, 12.5).SingleLine()
			}
		})
		ui.Spacer(a.c)
		if controls != nil {
			controls()
		}
	})
}

// banner is a coloured message strip.
func (a *App) banner(kind launcher.Kind, s string) {
	k := a.p.tone(kind)
	if kind == launcher.KindIdle {
		k = a.p.info
	}
	ui.Text(a.c, s).FontSize(12.5).TextColor(k.fg).Selectable().Padding(10, 14).Radius(10).Background(k.bg)
}

// emptyState is a centred message with an optional action.
func (a *App) emptyState(title, body string, action func()) {
	ui.Column(a.c).Padding(70, 20).AlignItems(ui.Center).Gap(6).Children(func() {
		ui.Text(a.c, title).FontSize(16).FontWeight(600)
		ui.Text(a.c, body).TextColor(a.p.muted).TextAlign(ui.Center).MaxWidth(420)
		if action != nil {
			ui.Box(a.c).Height(10)
			action()
		}
	})
}

// kv is one row of a definition list.
func (a *App) kv(k string, value func()) {
	ui.Text(a.c, k).FontSize(12.5).TextColor(a.p.muted)
	ui.Column(a.c).MinWidth(0).Children(value)
}
