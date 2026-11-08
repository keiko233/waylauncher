package gui

import (
	"context"
	"time"

	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/launcher"
)

// discScan is the window's side of one host's scan: whether it ran, is
// running, and how to stop it. The results live in the service
// (launcher.Service.DiscoveryOf).
type discScan struct {
	started   bool // a scan was started for this host (the automatic one happens once)
	busy      bool // ssh scan running
	iconsBusy bool // fetching icons after a successful scan
	cancelled bool // the last scan was cancelled by the user
	importing bool
	cancel    context.CancelFunc
}

// discoverUI is the Discover page's state.
type discoverUI struct {
	host  string
	scans map[string]*discScan
	sel   map[string]*bool // host + "\x00" + desktop ID -> checked
}

func (a *App) scanOf(host string) *discScan {
	if a.disc.scans == nil {
		a.disc.scans = map[string]*discScan{}
	}
	s := a.disc.scans[host]
	if s == nil {
		s = &discScan{}
		a.disc.scans[host] = s
	}
	return s
}

func (a *App) selected(host, id string) *bool {
	if a.disc.sel == nil {
		a.disc.sel = map[string]*bool{}
	}
	k := host + "\x00" + id
	if a.disc.sel[k] == nil {
		a.disc.sel[k] = new(bool)
	}
	return a.disc.sel[k]
}

// startScan scans a host off the main thread: the entries first, then the
// icons. Both are read-only ssh calls and can be cancelled; the UI thread is
// never blocked. It does nothing while a scan of that host is running.
func (a *App) startScan(host string) {
	s := a.scanOf(host)
	if s.busy || s.iconsBusy {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.started, s.busy, s.cancelled, s.cancel = true, true, false, cancel
	go func() {
		err := a.svc.ScanHost(ctx, host)
		scanned := err == nil
		a.later(func() {
			s.busy = false
			s.iconsBusy = scanned && ctx.Err() == nil
			if ctx.Err() != nil {
				s.cancelled = true
			}
		})
		if scanned && ctx.Err() == nil {
			_ = a.svc.LoadHostIcons(ctx, host) // best effort; the service keeps the error
		}
		a.later(func() {
			s.iconsBusy = false
			s.cancel = nil
			cancel()
			if ctx.Err() != nil {
				s.cancelled = true
			}
		})
	}()
}

func (a *App) cancelScan(host string) {
	if s := a.scanOf(host); s.cancel != nil {
		s.cancel()
	}
}

func (a *App) importSelected(host string, ids []string) {
	s := a.scanOf(host)
	s.importing = true
	go func() {
		res, err := a.svc.ImportDiscovered(host, ids)
		a.later(func() {
			s.importing = false
			switch {
			case err != nil:
				a.toastErr(err)
			case len(res.Imported) == 0:
				a.toast(i18n.T("gui.discover.toast.none"))
			default:
				for _, id := range ids {
					*a.selected(host, id) = false
				}
				a.toast(i18n.N("gui.discover.toast.imported", len(res.Imported)))
			}
		})
	}()
}

func (a *App) pageDiscover() {
	c, p, st := a.c, a.p, &a.st
	if len(st.Hosts) == 0 {
		a.header(i18n.T("gui.discover.title"), i18n.T("gui.discover.subtitle.idle"), nil)
		a.panelBox(ui.Column(c).Grow(1).Margin(0, 12, 12, 12)).Children(func() {
			a.emptyState(i18n.T("gui.discover.nohost.title"), i18n.T("gui.discover.nohost.body"), func() {
				if a.button(i18n.T("gui.discover.nohost.button"), btnPrimary).Height(32).Clicked() {
					a.openHost("")
				}
			})
		})
		return
	}
	if _, ok := a.hostInfo(a.disc.host); !ok {
		a.disc.host = st.Hosts[0].ID
	}
	host := a.disc.host
	scan := a.scanOf(host)
	// Opening the page scans the host once (read-only). Later scans are the
	// Scan button's.
	if !scan.started {
		a.startScan(host)
	}
	d := a.svc.DiscoveryOf(host)
	working := scan.busy || scan.iconsBusy

	sub := i18n.T("gui.discover.subtitle.idle")
	if d.Scanned {
		sub = i18n.T("gui.discover.subtitle.host", "apps", i18n.N("gui.main.count.app", len(d.Entries)), "host", host)
	}
	a.header(i18n.T("gui.discover.title"), sub, func() {
		if len(st.Hosts) > 1 {
			labels := make([]string, len(st.Hosts))
			cur := ""
			for i, h := range st.Hosts {
				labels[i] = h.ID
				if h.ID == host {
					cur = h.ID
				}
			}
			a.muted(i18n.T("gui.discover.host.label"), 12)
			if ui.Select(c.Key("disc-host"), &cur, labels).Label(i18n.T("gui.discover.host.label")).Width(160).Changed() {
				a.disc.host = cur
			}
		}
		switch {
		case scan.busy:
			if a.button(i18n.T("gui.discover.cancel"), btnPlain).Clicked() {
				a.cancelScan(host)
			}
			a.button(i18n.T("gui.discover.scanning"), btnPrimary).Disabled(true)
		default:
			label := i18n.T("gui.discover.scan")
			if d.Scanned {
				label = i18n.T("gui.discover.scanAgain")
			}
			if a.button(label, btnPrimary).Disabled(scan.iconsBusy).Clicked() {
				a.startScan(host)
			}
		}
	})
	if working {
		c.After(250 * time.Millisecond) // keep the spinner moving
	}

	var picks []launcher.DiscoverEntry // ticked and not yet imported
	for _, e := range d.Entries {
		if !e.Imported && *a.selected(host, e.ID) {
			picks = append(picks, e)
		}
	}

	ui.Column(c).Grow(1).MinHeight(0).Margin(0, 12, 12, 12).Gap(10).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).Gap(10).Children(func() {
			ui.RichText(c,
				ui.Span{Text: i18n.T("gui.discover.note.title"), Weight: 700, Color: p.text},
				ui.Span{Text: i18n.T("gui.discover.note.body")},
			).FontSize(12.5).TextColor(p.text2).Padding(12, 16).Radius(12).Background(p.note).Selectable()
			if d.Error != "" {
				a.discoverError(host, d)
			}
			if d.IconError != "" && !working {
				a.banner(launcher.KindIdle, i18n.T("gui.discover.iconsError", "error", firstLine(d.IconError)))
			}
			switch {
			case scan.busy && !d.Scanned:
				a.scanningState(host)
			case scan.cancelled && !d.Scanned:
				a.panelBox(ui.Column(c)).Children(func() {
					a.emptyState(i18n.T("gui.discover.cancelled.title"), i18n.T("gui.discover.cancelled.body"), nil)
				})
			case d.Scanned && len(d.Entries) == 0:
				a.panelBox(ui.Column(c)).Children(func() {
					a.emptyState(i18n.T("gui.discover.empty.title"), i18n.T("gui.discover.empty.body", "host", host), nil)
				})
			case len(d.Entries) > 0:
				a.discoverList(host, d, working)
			}
		})
		a.discoverFooter(host, d, picks, scan)
	})
}

func (a *App) scanningState(host string) {
	c := a.c
	a.panelBox(ui.Column(c)).Children(func() {
		ui.Column(c).Padding(60, 20).AlignItems(ui.Center).Gap(8).Children(func() {
			ui.Spinner(c).Label(i18n.T("gui.discover.scanning"))
			ui.Text(c, i18n.T("gui.discover.scanning.title", "host", host)).FontSize(16).FontWeight(600)
			ui.Text(c, i18n.T("gui.discover.scanning.body")).TextColor(a.p.muted).TextAlign(ui.Center).MaxWidth(420)
		})
	})
}

// discoverError shows a failed scan. For an untrusted or changed host key it
// offers the ssh command to run in Terminal; the app never accepts keys.
func (a *App) discoverError(host string, d launcher.Discovery) {
	c := a.c
	msg := i18n.T("gui.discover.error", "host", host, "error", firstLine(d.Error))
	if len(d.Entries) > 0 {
		msg += " " + i18n.T("gui.discover.error.keptOld")
	}
	a.banner(launcher.KindErr, msg)
	if f := d.Failure; f != nil && f.SSHCommand != "" {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, f.Message).FontSize(12.5).TextColor(a.p.text2).Selectable().Grow(1).MinWidth(0)
			if a.button(i18n.T("gui.discover.copyssh"), btnPlain).Clicked() {
				cmd := f.SSHCommand
				a.async(func() (string, error) {
					return i18n.T("gui.discover.toast.copied"), a.copyText(cmd)
				}, nil)
			}
		})
	}
}

func (a *App) discoverList(host string, d launcher.Discovery, working bool) {
	c, p := a.c, a.p
	if working {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Spinner(c).Label(i18n.T("gui.discover.iconsLoading"))
			a.muted(i18n.T("gui.discover.iconsLoading"), 12)
		})
	}
	a.panelBox(ui.Column(c).Padding(4, 0)).Children(func() {
		ui.Column(c).Dividers(1, p.hair).Children(func() {
			for _, e := range d.Entries {
				a.discoverRow(host, e)
			}
		})
	})
	if !d.At.IsZero() {
		a.muted(i18n.T("gui.discover.scannedAt", "time", clockShort(d.At)), 12).Margin(2, 4, 0)
	}
}

func (a *App) discoverRow(host string, e launcher.DiscoverEntry) {
	c, p := a.c, a.p
	glyph, tile := discoverGlyph(e)
	ui.Row(c.Key("disc-"+e.ID)).Gap(12).Padding(10, 18).AlignItems(ui.Center).Children(func() {
		ui.Checkbox(c.Key("sel-"+e.ID), a.selected(host, e.ID), "").
			Label(i18n.T("gui.discover.select", "name", e.Name)).Disabled(e.Imported).Shrink(0)
		a.appIcon(e.Icon, glyph, tile, 36, 9, 13)
		ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, e.Name).FontSize(13.5).FontWeight(600).SingleLine()
				if e.Imported {
					a.pill(launcher.KindOK, i18n.T("gui.discover.imported"), true)
				}
				if e.WaylandOnly {
					a.chip(i18n.T("gui.discover.chip.wayland"))
				}
				if e.Terminal {
					a.chip(i18n.T("gui.discover.chip.terminal"))
				}
			})
			if e.Comment != "" {
				a.muted(e.Comment, 12).SingleLine()
			}
			a.mono(e.CommandText, 11).TextColor(p.muted).SingleLine()
		})
	})
}

// discoverGlyph is the generated tile of an entry that has no icon: the same
// glyph and colour rule the window and the Dock use for a profile of that
// name, command and ID.
func discoverGlyph(e launcher.DiscoverEntry) (string, string) {
	id := e.ProfileID
	if e.Imported {
		id = e.ImportedAs
	}
	return launcher.GlyphFor(id, e.Name, e.Command)
}

func (a *App) discoverFooter(host string, d launcher.Discovery, picks []launcher.DiscoverEntry, scan *discScan) {
	c, p := a.c, a.p
	if len(d.Entries) == 0 {
		return
	}
	ui.Row(c).Gap(10).Padding(10, 16).Radius(12).AlignItems(ui.Center).Background(p.panel).Shrink(0).Children(func() {
		a.muted(i18n.N("gui.discover.selected", len(picks)), 12.5).FontWeight(600)
		if a.button(i18n.T("gui.discover.selectNew"), btnPlain).Clicked() {
			for _, e := range d.Entries {
				*a.selected(host, e.ID) = !e.Imported
			}
		}
		if a.button(i18n.T("gui.discover.selectNone"), btnPlain).Disabled(len(picks) == 0).Clicked() {
			for _, e := range d.Entries {
				*a.selected(host, e.ID) = false
			}
		}
		ui.Spacer(c)
		label := i18n.T("gui.discover.import")
		if scan.importing {
			label = i18n.T("gui.discover.importing")
		}
		if a.button(label, btnPrimary).Height(32).Disabled(len(picks) == 0 || scan.importing).Clicked() {
			ids := make([]string, len(picks))
			for i, e := range picks {
				ids[i] = e.ID
			}
			a.importSelected(host, ids)
		}
	})
}
