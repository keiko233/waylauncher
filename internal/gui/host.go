package gui

import (
	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/launcher"
)

func (a *App) hostInfo(id string) (launcher.HostInfo, bool) {
	for _, h := range a.st.Hosts {
		if h.ID == id {
			return h, true
		}
	}
	return launcher.HostInfo{}, false
}

func (a *App) runChecks(id string) {
	a.checking[id] = true
	go func() {
		_, err := a.svc.RunChecks(id) // stored by the service; Refresh follows
		a.later(func() {
			delete(a.checking, id)
			if err != nil {
				a.toastErr(err)
			}
		})
	}()
}

func (a *App) pageHost() {
	c, p := a.c, a.p
	h, ok := a.hostInfo(a.hostID)
	if !ok {
		a.header(i18n.T("gui.main.host.title"), "", nil)
		a.panelBox(ui.Column(c).Grow(1).Margin(0, 12, 12, 12)).Children(func() {
			a.emptyState(i18n.T("gui.main.host.empty.title"), i18n.T("gui.main.host.empty.body"), func() {
				if a.button(i18n.T("gui.main.host.empty.button"), btnPrimary).Height(32).Clicked() {
					a.openHost("")
				}
			})
		})
		return
	}
	r, has := a.svc.Report(h.ID)
	busy := a.checking[h.ID]
	a.header(h.ID, h.SSH, func() {
		if has {
			a.muted(i18n.T("gui.main.host.checked", "time", clockShort(r.At)), 12)
		} else {
			a.muted(i18n.T("gui.main.host.notchecked"), 12)
		}
		if a.button(i18n.T("gui.main.host.edit"), btnPlain).Clicked() {
			a.openHost(h.ID)
		}
		label := i18n.T("gui.main.host.runchecks")
		if busy {
			label = i18n.T("gui.main.host.checking")
		}
		if a.button(label, btnPrimary).Disabled(busy).Clicked() {
			a.runChecks(h.ID)
		}
	})

	co := a.st.Cocoa
	if has {
		co = r.Cocoa
	}
	ui.Scroll(c).Grow(1).MinHeight(0).Margin(0, 12, 12, 12).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Start).Children(func() {
			a.panelBox(ui.Column(c).Grow(1).Basis(0).MinWidth(0).Padding(18)).Children(func() {
				ui.Text(c, i18n.T("gui.main.host.checks.title")).FontSize(14).Bold().Margin(0, 0, 8)
				if !has {
					a.muted(i18n.T("gui.main.host.checks.none"), 12.5).Padding(6, 0)
				}
				for _, ch := range r.Checks {
					a.checkRow(ch)
				}
			})
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Gap(10).Children(func() {
				a.cocoaPanel(co)
				if has && r.Failure != nil {
					a.failurePanel(r.Failure)
				}
				ui.RichText(c,
					ui.Span{Text: i18n.T("gui.main.host.local.title"), Weight: 700, Color: p.text},
					ui.Span{Text: i18n.T("gui.main.host.local.body")},
				).FontSize(12.5).TextColor(p.text2).Padding(14, 18).Radius(12).Background(p.note).Selectable()
				ui.Row(c).Children(func() {
					if a.button(i18n.T("gui.main.host.local.button"), btnPlain).Clicked() {
						a.async(func() (string, error) { return "", openLocalNetworkSettings() }, nil)
					}
				})
			})
		})
	})
}

func (a *App) checkRow(ch launcher.Check) {
	c, p := a.c, a.p
	kind := ch.Kind
	k := p.tone(kind)
	mark := "i"
	switch kind {
	case launcher.KindOK:
		mark = "✓"
	case launcher.KindErr, launcher.KindWarn:
		mark = "!"
	default:
		k = p.info
	}
	ui.Row(c.Key("chk-"+ch.ID)).Gap(12).Padding(10, 0).AlignItems(ui.Start).Children(func() {
		ui.Box(c).Size(20, 20).Radius(10).Shrink(0).Center().Background(k.bg).Children(func() {
			ui.Text(c, mark).FontSize(12).Bold().TextColor(k.fg)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Text(c, ch.Title).FontSize(13).FontWeight(600)
			ui.Text(c, ch.Detail).FontSize(12).TextColor(p.muted).Selectable()
		})
		ui.Text(c, ch.Result).FontSize(12).FontWeight(600).TextColor(k.fg).MaxWidth(150).TextAlign(ui.End)
	})
}

func (a *App) cocoaPanel(co launcher.CocoaInfo) {
	c, p := a.c, a.p
	a.panelBox(ui.Column(c).Padding(18)).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			dot := p.warn.fg
			if co.Reachable {
				dot = p.ok.fg
			}
			a.cocoaMark(22, dot)
			ui.Text(c, "Cocoa-Way").FontSize(14).Bold()
			if co.Reachable {
				a.pill(launcher.KindOK, i18n.T("gui.main.host.cocoa.ready"), true)
			} else {
				a.pill(launcher.KindWarn, i18n.T("gui.main.host.cocoa.notrunning"), true)
			}
			ui.Spacer(c)
			if a.button(i18n.T("gui.main.host.cocoa.open"), btnPlain).Clicked() {
				a.async(func() (string, error) { return "", a.svc.OpenCocoaWay() }, nil)
			}
		})
		ui.Grid(c).ColumnTracks(ui.Fixed(130), ui.Fr(1)).GapY(8).Padding(12, 0, 0).Selectable().Children(func() {
			a.kv(i18n.T("gui.main.host.cocoa.socket"), func() { a.mono(co.Socket, 11.5) })
			a.kv(i18n.T("gui.main.host.cocoa.protocol"), func() {
				ui.Text(c, i18n.T("gui.main.host.cocoa.protocol.value", "protocol", co.Protocol)).FontSize(12.5)
			})
			if co.Version != "" {
				v := co.Version
				if co.PID != 0 {
					v = i18n.T("gui.main.host.cocoa.version.pid", "version", v, "pid", co.PID)
				}
				a.kv(i18n.T("gui.main.host.cocoa.version"), func() { ui.Text(c, v).FontSize(12.5) })
			}
			disp := "—"
			switch {
			case co.Display != "":
				disp = co.Display
			case co.Reachable:
				disp = i18n.T("gui.main.host.cocoa.display.later")
			}
			a.kv(i18n.T("gui.main.host.cocoa.display"), func() { a.mono(disp, 11.5) })
			a.kv(i18n.T("gui.main.host.cocoa.window"), func() {
				ui.Text(c, i18n.T("gui.main.host.cocoa.window.value")).FontSize(12.5).TextColor(p.warn.fg)
			})
			if co.Error != "" && !co.Reachable {
				a.kv(i18n.T("gui.main.host.cocoa.error"), func() { ui.Text(c, co.Error).FontSize(12.5).TextColor(p.err.fg) })
			}
		})
	})
}

func (a *App) failurePanel(f *launcher.Failure) {
	c, p := a.c, a.p
	a.panelBox(ui.Row(c).Padding(18).Gap(10).AlignItems(ui.Start)).Children(func() {
		ui.Box(c).Size(20, 20).Radius(10).Shrink(0).Center().Background(p.err.bg).Children(func() {
			ui.Text(c, "!").FontSize(12).Bold().TextColor(p.err.fg)
		})
		ui.Column(c).Grow(1).MinWidth(0).Gap(6).Children(func() {
			ui.Text(c, f.Title).FontSize(13.5).Bold()
			ui.Text(c, f.Message).FontSize(12.5).TextColor(p.text2).Selectable()
			ui.Row(c).Gap(8).Wrap().Margin(4, 0, 0).Children(func() {
				if f.SSHCommand != "" && a.button(i18n.T("gui.main.host.copyssh"), btnPlain).Clicked() {
					cmd := f.SSHCommand
					a.async(func() (string, error) {
						return i18n.T("gui.main.host.toast.copied"), a.copyText(cmd)
					}, nil)
				}
			})
		})
	})
}
