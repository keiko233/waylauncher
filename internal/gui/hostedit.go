package gui

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/launcher"
)

type hostEditor struct {
	m        launcher.HostEdit
	touched  map[string]bool
	sig      string
	problems []launcher.Problem
	saving   bool
}

func (a *App) openHost(id string) {
	m := launcher.HostEdit{IsNew: true}
	if id != "" {
		if h, ok := a.hostInfo(id); ok {
			m = launcher.HostEdit{ID: h.ID, SSH: h.SSH}
		}
	} else if len(a.st.Hosts) == 0 {
		m.ID = "vm200"
	}
	a.host = &hostEditor{m: m, touched: map[string]bool{}}
}

func (e *hostEditor) err(field string) string {
	if !e.touched[field] && field != "general" {
		return ""
	}
	var msgs []string
	for _, p := range e.problems {
		if p.Field == field {
			msgs = append(msgs, p.Message)
		}
	}
	return strings.Join(msgs, " ")
}

func (a *App) hostSheet() {
	e := a.host
	if e == nil {
		return
	}
	c, p := a.c, a.p
	if sig := fmt.Sprintf("%v", e.m); sig != e.sig {
		e.sig = sig
		e.problems = a.svc.ValidateHost(e.m)
	}
	open := true
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.scrim)
		a.sheetPanel(panel, 460)
		title := "Add host"
		if !e.m.IsNew {
			title = "Edit host"
		}
		ui.Column(c).Padding(20, 26, 14).Children(func() {
			ui.Text(c, title).FontSize(17).Bold()
			a.muted("The machine your applications run on", 12)
		})
		ui.Divider(c).Background(p.hair)
		ui.Grid(c).ColumnTracks(ui.Fixed(90), ui.Fr(1)).GapX(16).GapY(14).Padding(18, 26).Children(func() {
			lbl := a.label("ID")
			ui.Column(c).Children(func() {
				in := ui.TextInput(c.Key("h-id"), &e.m.ID).LabelledBy(lbl).Font("monospace").Placeholder("vm200").Disabled(!e.m.IsNew).Invalid(e.err("id") != "")
				if e.m.IsNew {
					in.AutoFocus()
				}
				if in.Changed() {
					e.touched["id"] = true
				}
				a.errText(e.err("id"))
			})
			lbl = a.label("SSH target")
			ui.Column(c).Children(func() {
				in := ui.TextInput(c.Key("h-ssh"), &e.m.SSH).LabelledBy(lbl).Font("monospace").Placeholder("user@10.0.0.9 or a name from ~/.ssh/config").Invalid(e.err("ssh") != "")
				if in.Changed() {
					e.touched["ssh"] = true
				}
				a.help("Host keys are checked against your known_hosts and never accepted automatically. No passwords: use ssh keys.")
				a.errText(e.err("ssh"))
				a.errText(e.err("general"))
			})
		})
		ui.Divider(c).Background(p.hair)
		ui.Row(c).Gap(10).Padding(14, 26).AlignItems(ui.Center).Background(p.note).Children(func() {
			if !e.m.IsNew {
				if a.button("Remove…", btnDangerText).Height(32).Clicked() {
					id := e.m.ID
					a.confirmThen("Remove host "+id+"?",
						"This removes it from profiles.toml. Applications that still use it must be removed or moved first.", "Remove", func() {
							a.async(func() (string, error) { return "Removed " + id + ".", a.svc.RemoveHost(id) }, func() {
								a.host = nil
								if a.page == PageHost && a.hostID == id {
									a.page = PageApps
								}
							})
						})
				}
			}
			ui.Spacer(c)
			if a.button("Cancel", btnPlain).Height(32).Clicked() {
				open = false
			}
			label := "Save"
			if e.m.IsNew {
				label = "Add host"
			}
			if a.button(label, btnPrimary).Height(32).Disabled(len(e.problems) > 0 || e.saving).Clicked() {
				a.saveHost(e)
			}
		})
	})
	if !open {
		a.host = nil
	}
}

func (a *App) saveHost(e *hostEditor) {
	e.saving = true
	m := e.m
	m.ID, m.SSH = strings.TrimSpace(m.ID), strings.TrimSpace(m.SSH)
	go func() {
		problems, err := a.svc.SaveHost(m)
		a.later(func() {
			e.saving = false
			switch {
			case err != nil:
				a.toastErr(err)
			case len(problems) > 0:
				e.touched["id"], e.touched["ssh"] = true, true
				e.problems = problems
			default:
				if a.host == e {
					a.host = nil
				}
				if a.ed != nil && a.ed.m.IsNew && a.ed.m.Host == "" {
					a.ed.m.Host = m.ID
				}
				if m.IsNew {
					a.toast("Added host " + m.ID + ".")
				} else {
					a.toast("Saved " + m.ID + ".")
				}
			}
		})
	}()
}
