package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/launcher"
)

func (a *App) visibleApps() []launcher.AppInfo {
	q := strings.ToLower(strings.TrimSpace(a.query))
	var out []launcher.AppInfo
	for _, ap := range a.st.Apps {
		if a.filter == 1 && !ap.Pinned {
			continue
		}
		if q != "" {
			hit := false
			for _, t := range []string{ap.Name, ap.ID, ap.CommandText, ap.Host} {
				if strings.Contains(strings.ToLower(t), q) {
					hit = true
				}
			}
			if !hit {
				continue
			}
		}
		out = append(out, ap)
	}
	return out
}

func (a *App) pageApps() {
	c, st := a.c, &a.st
	apps := a.visibleApps()
	appsText := i18n.N("gui.main.count.app", len(st.Apps))
	live := i18n.N("gui.main.count.instance", st.Live)
	var sub string
	switch {
	case len(st.Hosts) == 1:
		sub = i18n.T("gui.main.apps.subtitle.host", "apps", appsText, "host", st.Hosts[0].ID, "live", live)
	case len(st.Hosts) > 1:
		sub = i18n.T("gui.main.apps.subtitle.host", "apps", appsText, "host", i18n.N("gui.main.count.host", len(st.Hosts)), "live", live)
	default:
		sub = i18n.T("gui.main.apps.subtitle", "apps", appsText, "live", live)
	}

	a.header(i18n.T("gui.main.apps.title"), sub, func() {
		ui.Segmented(c, &a.filter, i18n.T("gui.main.apps.filter.all"), i18n.T("gui.main.apps.filter.pinned")).Label(i18n.T("gui.main.apps.filter.label"))
		if a.iconButton(icPlus, i18n.T("gui.main.apps.new.short"), btnPrimary).Disabled(len(st.Hosts) == 0).Clicked() {
			a.openEditor("")
		}
	})

	w, _ := c.Size()
	avail := w - sidebarWidth - 1 - 24 - 36
	cols := int((avail + 14) / (290 + 14))
	if cols < 1 {
		cols = 1
	}

	ui.Scroll(c).Grow(1).Padding(8, 24, 22).Gap(14).Children(func() {
		if st.Profiles.Error != "" {
			a.banner(launcher.KindErr, i18n.T("gui.main.apps.error.profiles", "error", st.Profiles.Error))
		}
		if st.Profiles.Notice != "" {
			a.banner(launcher.KindIdle, st.Profiles.Notice)
		}
		for _, w := range st.Profiles.Warnings {
			a.banner(launcher.KindIdle, w)
		}
		switch {
		case len(st.Hosts) == 0:
			a.emptyState(i18n.T("gui.main.apps.empty.nohost.title"),
				i18n.T("gui.main.apps.empty.nohost.body"),
				func() {
					if a.button(i18n.T("gui.main.apps.empty.nohost.button"), btnPrimary).Height(32).Clicked() {
						a.openHost("")
					}
				})
		case len(st.Apps) == 0:
			a.emptyState(i18n.T("gui.main.apps.empty.noapps.title"),
				i18n.T("gui.main.apps.empty.noapps.body", "host", st.Hosts[0].ID),
				func() {
					if a.button(i18n.T("gui.main.apps.empty.noapps.button"), btnPrimary).Height(32).Clicked() {
						a.openEditor("")
					}
				})
		case len(apps) == 0:
			body := i18n.T("gui.main.apps.empty.nomatch.pinned")
			if a.query != "" {
				body = i18n.T("gui.main.apps.empty.nomatch.query", "query", a.query)
			}
			a.emptyState(i18n.T("gui.main.apps.empty.nomatch.title"), body, nil)
		default:
			ui.Grid(c).Columns(cols).Gap(14).Children(func() {
				for _, ap := range apps {
					a.appCard(ap)
				}
			})
		}
		a.muted(i18n.T("gui.main.apps.note.connected"), 12).Margin(6, 4, 0)
	})
}

// appCard is one application: a flat card with the soft fill and a 1px border.
func (a *App) appCard(ap launcher.AppInfo) {
	c, p := a.c, a.p
	ui.Column(c.Key("card-"+ap.ID)).Padding(16).Gap(12).Radius(12).Background(p.card).Border(1, p.cardLine).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			a.appIcon(ap.Icon, ap.Glyph, ap.Tile, 48, 12, 16)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, ap.Name).FontSize(14.5).FontWeight(600).SingleLine()
					if ap.Pinned {
						ui.Icon(c, icPin).FontSize(12).TextColor(p.muted).Tooltip(i18n.T("gui.main.apps.card.pinned"))
					}
				})
				a.mono(ap.CommandText, 11.5).TextColor(p.muted).SingleLine()
			})
			a.cardMenu(ap)
		})
		ui.Row(c).Gap(6).Wrap().Children(func() {
			a.chip(ap.Host)
			a.chip(ap.Transport)
			if ap.Instances == "multiple" {
				a.chip(i18n.T("gui.main.apps.card.multiple"))
			} else {
				a.chip(i18n.T("gui.main.apps.card.single"))
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Shrink(1).MinWidth(0).Children(func() { a.pill(ap.StatusKind, ap.StatusLabel, false) })
			ui.Spacer(c)
			switch {
			case ap.Action == "start-again":
				if a.keyedButton("open-"+ap.ID, i18n.T("gui.main.apps.card.startagain"), btnPrimary).Clicked() {
					a.openApp(ap.ID, false)
				}
			default:
				if a.keyedButton("open-"+ap.ID, i18n.T("gui.main.apps.card.open"), btnPlain).Clicked() {
					a.openApp(ap.ID, false)
				}
				if ap.Live > 0 && ap.Instances == "multiple" {
					if a.keyedButton("new-"+ap.ID, i18n.T("gui.main.apps.card.newwindow"), btnPlain).Clicked() {
						a.openApp(ap.ID, true)
					}
				}
			}
		})
	})
}

func (a *App) cardMenu(ap launcher.AppInfo) {
	c, p := a.c, a.p
	more := ui.ButtonBase(c.Key("more-"+ap.ID)).Size(28, 28).Radius(8).Center().Shrink(0).
		Label(i18n.T("gui.main.apps.menu.label", "name", ap.Name)).TextColor(p.muted)
	if more.Hovered() {
		more.Background(p.hover)
	}
	more.Children(func() { ui.Icon(c, icDots).FontSize(16) })
	more.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item(i18n.T("gui.main.apps.menu.startagain")).Disabled(ap.Action != "start-again").Chosen() {
			a.openApp(ap.ID, false)
		}
		if m.Item(i18n.T("gui.main.apps.menu.lastlog")).Chosen() {
			a.async(func() (string, error) { return "", a.openLastLog(ap.ID) }, nil)
		}
		m.Separator()
		if m.Item(i18n.T("gui.main.apps.menu.edit")).Chosen() {
			a.openEditor(ap.ID)
		}
		pin := i18n.T("gui.main.apps.menu.pin")
		if ap.Pinned {
			pin = i18n.T("gui.main.apps.menu.unpin")
		}
		if m.Item(pin).Chosen() {
			want := !ap.Pinned
			a.async(func() (string, error) {
				if err := a.svc.SetPinned(ap.ID, want); err != nil {
					return "", err
				}
				if want {
					return i18n.T("gui.main.apps.toast.pinned"), nil
				}
				return i18n.T("gui.main.apps.toast.unpinned"), nil
			}, nil)
		}
		m.Separator()
		if m.Item(i18n.T("gui.main.apps.menu.remove")).Chosen() {
			a.confirmThen(i18n.T("gui.main.apps.remove.title", "name", ap.Name),
				i18n.T("gui.main.apps.remove.body"), i18n.T("gui.main.apps.remove.yes"), func() {
					a.async(func() (string, error) {
						return i18n.T("gui.main.apps.toast.removed", "name", ap.Name), a.svc.RemoveApp(ap.ID)
					}, nil)
				})
		}
	}, ui.PopoverMenuButtonOptions{At: ui.AnchorBottomEnd, Self: ui.AnchorTopEnd})
}

// openApp starts or focuses an application (never restarts one by itself).
func (a *App) openApp(id string, newWindow bool) {
	a.async(func() (string, error) {
		var r launcher.OpenResult
		var err error
		if newWindow {
			r, err = a.svc.NewWindow(id)
		} else {
			r, err = a.svc.Open(id)
		}
		return r.Note, err
	}, nil)
}
