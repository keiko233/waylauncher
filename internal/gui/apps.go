package gui

import (
	"strings"

	"github.com/egoist/mygo/ui"

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
	hostText := ""
	switch {
	case len(st.Hosts) == 1:
		hostText = " on " + st.Hosts[0].ID
	case len(st.Hosts) > 1:
		hostText = " on " + plural(len(st.Hosts), "host")
	}
	sub := plural(len(st.Apps), "app") + hostText + " · " + plural(st.Live, "instance") + " running"

	a.header("Applications", sub, func() {
		ui.Segmented(c, &a.filter, "All", "Pinned").Label("Filter")
		if a.iconButton(icPlus, "New app", btnPrimary).Disabled(len(st.Hosts) == 0).Clicked() {
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
			a.banner(launcher.KindErr, "The profiles could not be loaded or are invalid. Nothing was changed on disk.\n"+st.Profiles.Error)
		}
		if st.Profiles.Notice != "" {
			a.banner(launcher.KindIdle, st.Profiles.Notice)
		}
		for _, w := range st.Profiles.Warnings {
			a.banner(launcher.KindIdle, w)
		}
		switch {
		case len(st.Hosts) == 0:
			a.emptyState("Add a host to get started",
				"A host is the Linux machine your applications run on, as user@address or a name from your ssh config. Waylauncher never stores passwords; it uses your ssh keys and known_hosts.",
				func() {
					if a.button("Add host…", btnPrimary).Height(32).Clicked() {
						a.openHost("")
					}
				})
		case len(st.Apps) == 0:
			a.emptyState("No applications yet",
				"Add the first application: a command to run on "+st.Hosts[0].ID+", such as konsole --separate --nofork.",
				func() {
					if a.button("New application…", btnPrimary).Height(32).Clicked() {
						a.openEditor("")
					}
				})
		case len(apps) == 0:
			body := "No applications are pinned. Use “Pin to menu bar” in an application’s ⋯ menu."
			if a.query != "" {
				body = "No application matches “" + a.query + "”."
			}
			a.emptyState("Nothing matches", body, nil)
		default:
			ui.Grid(c).Columns(cols).Gap(14).Children(func() {
				for _, ap := range apps {
					a.appCard(ap)
				}
			})
		}
		a.muted("“Connected” means the Waypipe connection is alive. It doesn’t confirm that a window is on screen: Cocoa-Way doesn’t report windows yet.", 12).Margin(6, 4, 0)
	})
}

// appCard is one application: a flat card with the soft fill and a 1px border.
func (a *App) appCard(ap launcher.AppInfo) {
	c, p := a.c, a.p
	ui.Column(c.Key("card-"+ap.ID)).Padding(16).Gap(12).Radius(12).Background(p.card).Border(1, p.cardLine).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Start).Children(func() {
			a.tile(ap.Glyph, ap.Tile, 48, 12, 16)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, ap.Name).FontSize(14.5).FontWeight(600).SingleLine()
					if ap.Pinned {
						ui.Icon(c, icPin).FontSize(12).TextColor(p.muted).Tooltip("Pinned to the menu bar")
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
				a.chip("Multiple instances")
			} else {
				a.chip("Single instance")
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Shrink(1).MinWidth(0).Children(func() { a.pill(ap.StatusKind, ap.StatusLabel, false) })
			ui.Spacer(c)
			switch {
			case ap.Action == "start-again":
				if a.keyedButton("open-"+ap.ID, "Start again", btnPrimary).Clicked() {
					a.openApp(ap.ID, false)
				}
			default:
				if a.keyedButton("open-"+ap.ID, "Open", btnPlain).Clicked() {
					a.openApp(ap.ID, false)
				}
				if ap.Live > 0 && ap.Instances == "multiple" {
					if a.keyedButton("new-"+ap.ID, "New window", btnPlain).Clicked() {
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
		Label("More actions for " + ap.Name).TextColor(p.muted)
	if more.Hovered() {
		more.Background(p.hover)
	}
	more.Children(func() { ui.Icon(c, icDots).FontSize(16) })
	more.PopoverMenuButton(func(m *ui.Menu) {
		if m.Item("Start again").Disabled(ap.Action != "start-again").Chosen() {
			a.openApp(ap.ID, false)
		}
		if m.Item("Open last log").Chosen() {
			a.async(func() (string, error) { return "", a.openLastLog(ap.ID) }, nil)
		}
		m.Separator()
		if m.Item("Edit…").Chosen() {
			a.openEditor(ap.ID)
		}
		pin := "Pin to menu bar"
		if ap.Pinned {
			pin = "Unpin from menu bar"
		}
		if m.Item(pin).Chosen() {
			want := !ap.Pinned
			a.async(func() (string, error) {
				if err := a.svc.SetPinned(ap.ID, want); err != nil {
					return "", err
				}
				if want {
					return "Pinned to the menu bar.", nil
				}
				return "Unpinned.", nil
			}, nil)
		}
		m.Separator()
		if m.Item("Remove from Waylauncher…").Chosen() {
			a.confirmThen("Remove "+ap.Name+"?",
				"This removes the profile from profiles.toml (a backup is kept). Nothing is changed on the host.", "Remove", func() {
					a.async(func() (string, error) { return "Removed " + ap.Name + ".", a.svc.RemoveApp(ap.ID) }, nil)
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
