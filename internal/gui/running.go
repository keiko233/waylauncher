package gui

import (
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/launcher"
)

var runCols = []ui.Track{ui.Fr(1.5), ui.Fr(1.6), ui.Fr(0.7), ui.Fr(1.35)}

func (a *App) instance(id string) (launcher.InstanceInfo, bool) {
	for _, in := range a.st.Instances {
		if in.ID == id {
			return in, true
		}
	}
	return launcher.InstanceInfo{}, false
}

func (a *App) pageRunning() {
	c, p, st := a.c, a.p, &a.st
	if _, ok := a.instance(a.selInst); !ok {
		a.selInst = ""
		if len(st.Instances) > 0 {
			a.selInst = st.Instances[0].ID
		}
	}
	a.header(i18n.T("gui.main.running.title"), i18n.T("gui.main.running.subtitle", "live", st.Live, "ended", st.Ended), func() {
		if a.button(i18n.T("gui.main.running.clear"), btnPlain).Disabled(st.Ended == 0).Clicked() {
			a.async(func() (string, error) { a.svc.ClearEnded(); return "", nil }, nil)
		}
	})
	// "Started … ago" moves on.
	c.After(30 * time.Second)

	ui.Row(c).Grow(1).MinHeight(0).Margin(0, 12, 12, 12).Gap(10).AlignItems(ui.Stretch).Children(func() {
		a.panelBox(ui.Column(c).Grow(1).MinWidth(0).Clip()).Children(func() {
			ui.Grid(c).ColumnTracks(runCols...).GapX(12).Padding(12, 18).Children(func() {
				for _, h := range []string{i18n.T("gui.main.running.col.instance"), i18n.T("gui.main.running.col.state"), i18n.T("gui.main.running.col.started")} {
					ui.Text(c, h).FontSize(11.5).FontWeight(600).TextColor(p.muted)
				}
				ui.Text(c, i18n.T("gui.main.running.col.actions")).FontSize(11.5).FontWeight(600).TextColor(p.muted).TextAlign(ui.End)
			})
			ui.Divider(c).Background(p.hair)
			ui.Scroll(c).Grow(1).MinHeight(0).Children(func() {
				if len(st.Instances) == 0 {
					ui.Box(c).Padding(30).Children(func() {
						a.muted(i18n.T("gui.main.running.empty"), 13).TextAlign(ui.Center)
					})
				}
				for _, in := range st.Instances {
					a.instRow(in)
				}
				a.runNote()
			})
		})
		a.detailPane()
	})
}

func (a *App) runNote() {
	p := a.p
	rt := []ui.Span{
		{Text: i18n.T("gui.main.running.note.title"), Weight: 700, Color: p.text},
		{Text: i18n.T("gui.main.running.note.body")},
	}
	ui.RichText(a.c, rt...).FontSize(12).TextColor(p.text2).Selectable().Margin(14).Padding(12, 14).Radius(12).Background(p.note)
}

func (a *App) instRow(in launcher.InstanceInfo) {
	c, p := a.c, a.p
	sel := in.ID == a.selInst
	row := ui.Grid(c.Key("inst-"+in.ID)).ColumnTracks(runCols...).GapX(12).Padding(13, 18).AlignItems(ui.Center)
	if sel {
		row.Background(p.rowSel)
	}
	row.Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).MinWidth(0).Children(func() {
			a.appIcon(in.Icon, in.Glyph, in.Tile, 32, 8, 12)
			ui.Column(c).MinWidth(0).Children(func() {
				ui.Text(c, in.AppName).FontSize(13.5).FontWeight(600).SingleLine()
				a.mono(in.ID, 11).TextColor(p.muted).SingleLine()
			})
		})
		ui.Column(c).Gap(4).MinWidth(0).Children(func() {
			ui.Row(c).Children(func() { a.pill(in.Kind, in.Label, true) })
			a.muted(in.Detail, 11.5)
		})
		ui.Text(c, clockShort(in.StartedAt)).FontSize(12.5).TextColor(p.text2)
		ui.Row(c).Gap(6).Wrap().Justify(ui.End).Children(func() { a.instActions(in) })
	})
	if row.Clicked() {
		a.selInst = in.ID
	}
}

func (a *App) instActions(in launcher.InstanceInfo) {
	k := "-" + in.ID
	btn := func(id, label string, kind btnKind, fn func()) {
		if a.keyedButton(id+k, label, kind).Clicked() {
			a.selInst = in.ID
			fn()
		}
	}
	log := func() { a.async(func() (string, error) { return "", a.openLog(in.ID) }, nil) }
	again := func() { a.startAgain(in.ID) }
	dismiss := func() { a.async(func() (string, error) { return "", a.svc.Dismiss(in.ID) }, nil) }
	switch in.State {
	case "connecting":
		btn("cancel", i18n.T("gui.main.running.btn.cancel"), btnPlain, func() { a.async(func() (string, error) { return "", a.svc.Stop(in.ID) }, nil) })
	case "connected":
		btn("focus", i18n.T("gui.main.running.btn.focus"), btnPlain, func() { a.focus(in.ID) })
		btn("log", i18n.T("gui.main.running.btn.log"), btnPlain, log)
	case "stopping":
		a.muted(i18n.T("gui.main.running.stopping"), 12)
	case "disconnected", "failed":
		btn("again", i18n.T("gui.main.running.btn.startagain"), btnPrimary, again)
		btn("log", i18n.T("gui.main.running.btn.log"), btnPlain, log)
		btn("dismiss", i18n.T("gui.main.running.btn.dismiss"), btnPlain, dismiss)
	default:
		btn("log", i18n.T("gui.main.running.btn.log"), btnPlain, log)
		btn("dismiss", i18n.T("gui.main.running.btn.dismiss"), btnPlain, dismiss)
	}
}

func (a *App) focus(id string) {
	a.async(func() (string, error) { return a.svc.Focus(id) }, nil)
}

// startAgain starts a NEW instance on the user's request; the old session
// cannot be recovered.
func (a *App) startAgain(id string) {
	go func() {
		r, err := a.svc.StartAgain(id)
		a.later(func() {
			if err != nil {
				a.toastErr(err)
				return
			}
			a.selInst = r.InstanceID
			if r.Note != "" {
				a.toast(r.Note)
			}
		})
	}()
}

func (a *App) stopConfirm(in launcher.InstanceInfo) {
	prog := in.AppName
	for _, ap := range a.st.Apps {
		if ap.ID == in.AppID && len(ap.Command) > 0 {
			prog = ap.Command[0]
			for i := len(prog) - 1; i >= 0; i-- {
				if prog[i] == '/' {
					prog = prog[i+1:]
					break
				}
			}
		}
	}
	a.confirmThen(i18n.T("gui.main.running.stop.title", "name", in.AppName),
		i18n.T("gui.main.running.stop.body", "prog", prog, "host", in.HostID), i18n.T("gui.main.running.stop.yes"), func() {
			a.async(func() (string, error) { return "", a.svc.Stop(in.ID) }, nil)
		})
}

// tailLog refreshes the log text of the selected instance at most once a second.
func (a *App) tailLog(in launcher.InstanceInfo) string {
	if in.LogPath == "" {
		return i18n.T("gui.main.running.log.none")
	}
	if a.logInst != in.ID || a.c.Now().Sub(a.logAt) >= time.Second {
		t := a.svc.LogTail(in.ID)
		if t == "" {
			t = i18n.T("gui.main.running.log.empty")
		}
		if a.logInst != in.ID || t != a.logText {
			follow := a.logInst != in.ID || a.logScrol.Y >= a.logScrol.MaxY-8
			a.logText, a.logInst = t, in.ID
			if follow {
				a.logScrol.Y = 1 << 30
			}
		}
		a.logAt = a.c.Now()
	}
	a.c.After(time.Second)
	return a.logText
}

func (a *App) detailPane() {
	c, p := a.c, a.p
	in, ok := a.instance(a.selInst)
	col := a.panelBox(ui.Column(c).Width(340).Shrink(0).Clip())
	col.Children(func() {
		if !ok {
			ui.Box(c).Grow(1).Center().Padding(30).Children(func() {
				a.muted(i18n.T("gui.main.running.detail.select"), 13).TextAlign(ui.Center)
			})
			return
		}
		ui.Column(c).Padding(18, 18, 14).Gap(2).Children(func() {
			a.muted(i18n.T("gui.main.running.detail.selected"), 11.5).FontWeight(600)
			ui.Text(c, in.AppName).FontSize(16).Bold().Margin(2, 0, 0)
			a.mono(in.ID, 11.5).TextColor(p.muted)
		})
		ui.Divider(c).Background(p.hair)
		ui.Grid(c).ColumnTracks(ui.Fixed(96), ui.Fr(1)).GapY(8).Padding(12, 18).Selectable().Children(func() {
			k := p.tone(in.Kind)
			a.kv(i18n.T("gui.main.running.detail.state"), func() { ui.Text(c, in.Label).FontSize(12.5).FontWeight(600).TextColor(k.fg) })
			a.kv(i18n.T("gui.main.running.detail.window"), func() {
				a.muted(i18n.T("gui.main.running.detail.window.value"), 12.5).TextColor(p.text2)
			})
			a.kv(i18n.T("gui.main.running.detail.host"), func() { a.mono(in.HostSSH, 11.5) })
			a.kv(i18n.T("gui.main.running.detail.transport"), func() { ui.Text(c, in.Transport).FontSize(12.5) })
			a.kv(i18n.T("gui.main.running.detail.command"), func() { a.mono(in.Command, 11.5) })
			a.kv(i18n.T("gui.main.running.detail.started"), func() { ui.Text(c, clock(in.StartedAt)+", "+ago(in.StartedAt)).FontSize(12.5) })
			if in.ExitCode != nil {
				a.kv(i18n.T("gui.main.running.detail.exit"), func() { ui.Text(c, itoa(*in.ExitCode)).FontSize(12.5) })
			}
		})
		ui.Row(c).Padding(4, 18, 8).AlignItems(ui.Center).Children(func() {
			a.muted(i18n.T("gui.main.running.detail.log"), 11.5).FontWeight(600)
			ui.Spacer(c)
			if in.LogPath != "" {
				l := ui.ButtonBase(c.Key("reveal")).Label(i18n.T("gui.main.running.detail.reveal")).Padding(2, 4).Radius(5).TextColor(p.accent)
				l.Children(func() { ui.Text(c, i18n.T("gui.main.running.detail.reveal")).FontSize(12) })
				if l.Clicked() {
					a.async(func() (string, error) { return "", a.revealLog(in.ID) }, nil)
				}
			}
		})
		text := a.tailLog(in)
		ui.Scroll(c.Key("log")).TrackScroll(&a.logScrol).Grow(1).MinHeight(90).Margin(0, 18).Padding(10, 12).Radius(10).Background(p.codeBg).Children(func() {
			ui.Text(c, text).Font("monospace").FontSize(10.5).LineHeight(1.6).TextColor(p.codeText).Selectable()
		})
		ui.Row(c).Gap(8).Wrap().Padding(14, 18).Margin(12, 0, 0).Children(func() {
			ui.Box(c).Height(0)
			switch in.State {
			case "connected":
				if a.button(i18n.T("gui.main.running.btn.focus"), btnPlain).Clicked() {
					a.focus(in.ID)
				}
				if a.button(i18n.T("gui.main.running.btn.stop"), btnDangerText).Clicked() {
					a.stopConfirm(in)
				}
			case "connecting":
				if a.button(i18n.T("gui.main.running.btn.cancel"), btnPlain).Clicked() {
					a.async(func() (string, error) { return "", a.svc.Stop(in.ID) }, nil)
				}
			case "disconnected", "failed":
				if a.button(i18n.T("gui.main.running.btn.startagain"), btnPrimary).Clicked() {
					a.startAgain(in.ID)
				}
				if a.button(i18n.T("gui.main.running.btn.dismiss"), btnPlain).Clicked() {
					a.async(func() (string, error) { return "", a.svc.Dismiss(in.ID) }, nil)
				}
			}
		})
	})
}
