package gui

import (
	"fmt"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/launcher"
)

// appEditor is the open application editor. Its problems and preview come
// from the service (the same validation and command builder that launch).
type appEditor struct {
	m       launcher.AppEdit
	touched map[string]bool
	draft   string // the argument being typed

	sig      string
	problems []launcher.Problem
	preview  launcher.Preview
	saving   bool
}

func (a *App) openEditor(appID string) {
	var m launcher.AppEdit
	var err error
	if appID == "" {
		m = a.svc.NewEdit()
	} else if m, err = a.svc.GetEdit(appID); err != nil {
		a.toastErr(err)
		return
	}
	a.ed = &appEditor{m: m, touched: map[string]bool{}}
}

// splitArgs splits a typed command line into arguments with shell-like
// quoting. It never evaluates anything. pending reports an unclosed quote or
// escape.
func splitArgs(s string) (parts []string, pending bool) {
	var cur strings.Builder
	var q rune
	has := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		ch := rs[i]
		switch {
		case q != 0:
			switch {
			case ch == q:
				q = 0
			case ch == '\\' && q == '"' && i+1 < len(rs):
				i++
				cur.WriteRune(rs[i])
			default:
				cur.WriteRune(ch)
			}
		case ch == '"' || ch == '\'':
			q, has = ch, true
		case ch == '\\' && i+1 < len(rs):
			i++
			cur.WriteRune(rs[i])
			has = true
		case unicode.IsSpace(ch):
			if cur.Len() > 0 || has {
				parts = append(parts, cur.String())
			}
			cur.Reset()
			has = false
		default:
			cur.WriteRune(ch)
			has = true
		}
	}
	if cur.Len() > 0 || has {
		parts = append(parts, cur.String())
	}
	return parts, q != 0
}

// absorbDraft moves complete arguments out of the draft into the command.
// final also takes the last one (Enter, or leaving the field).
func (e *appEditor) absorb(text string, final bool) string {
	parts, pending := splitArgs(text)
	if len(parts) == 0 {
		return ""
	}
	trailingSpace := unicode.IsSpace([]rune(text)[len([]rune(text))-1]) && !pending
	keep := ""
	if !final && !trailingSpace {
		if len(parts) == 1 {
			return text // still typing the only argument
		}
		keep = parts[len(parts)-1]
		parts = parts[:len(parts)-1]
	}
	e.m.Command = append(e.m.Command, parts...)
	e.touched["command"] = true
	return keep
}

func (a *App) fieldErr(e *appEditor, field string) string {
	if !e.touched[field] && e.m.IsNew && field != "general" {
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

func (a *App) errText(s string) {
	if s != "" {
		ui.Text(a.c, s).FontSize(11.5).TextColor(a.p.dangerText).Margin(5, 0, 0)
	}
}

func (a *App) help(s string) {
	ui.Text(a.c, s).FontSize(11.5).TextColor(a.p.muted).Margin(5, 0, 0)
}

// label is a form label; controls name themselves from it with LabelledBy,
// so a click on it focuses the control.
func (a *App) label(s string) ui.Element {
	return ui.Text(a.c, s).FontSize(12.5).FontWeight(600).TextColor(a.p.text2).TextAlign(ui.End).Padding(7, 0, 0)
}

func (a *App) sheetPanel(panel ui.Element, width float32) {
	_, h := a.c.Size()
	panel.Width(width).MaxWidthPercent(100).MaxHeight(h-48).Radius(16).Background(a.p.card).Border(1, a.p.cardLine).Clip()
}

func (a *App) editorSheet() {
	e := a.ed
	if e == nil {
		return
	}
	c, p := a.c, a.p
	sig := fmt.Sprintf("%v", e.m)
	if sig != e.sig {
		e.sig = sig
		e.problems = a.svc.ValidateEdit(e.m)
		e.preview = a.svc.Preview(e.m)
	}
	open := true
	ui.DialogBase(c, &open, func(backdrop, panel ui.Element) {
		backdrop.Background(p.scrim)
		a.sheetPanel(panel, 760)
		title, sub := i18n.T("gui.forms.editor.newTitle"), i18n.T("gui.forms.editor.newSub")
		glyph, tile, icon := "+", "#23262E", ""
		if !e.m.IsNew {
			title, sub = e.m.Name, i18n.T("gui.forms.editor.editSub")
			if title == "" {
				title = e.m.ID
			}
			glyph, icon = ">_", e.m.Icon
			// The same icon as the card and the Dock bundle: the profile's
			// image, else the generated glyph tile.
			for _, ap := range a.st.Apps {
				if ap.ID == e.m.ID {
					glyph, tile = ap.Glyph, ap.Tile
				}
			}
		}
		ui.Row(c).Gap(14).Padding(20, 26, 14).AlignItems(ui.Center).Children(func() {
			a.appIcon(icon, glyph, tile, 44, 11, 15)
			ui.Column(c).Grow(1).Children(func() {
				ui.Text(c, title).FontSize(17).Bold().SingleLine()
				a.muted(sub, 12)
			})
		})
		ui.Divider(c).Background(p.hair)
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(18, 26).Children(func() { a.editorForm(e) })
		ui.Divider(c).Background(p.hair)
		ui.Row(c).Gap(10).Padding(14, 26).AlignItems(ui.Center).Background(p.note).Children(func() {
			if n := len(e.problems); n > 0 {
				ui.Text(c, i18n.N("gui.forms.editor.problems", n)).FontSize(12).FontWeight(600).TextColor(p.dangerText)
			}
			ui.Spacer(c)
			if a.button(i18n.T("gui.forms.cancel"), btnPlain).Height(32).Clicked() {
				open = false
			}
			save := a.button(i18n.T("gui.forms.save"), btnPrimary).Height(32).Disabled(len(e.problems) > 0 || e.saving)
			if save.Clicked() {
				a.saveEditor(e)
			}
		})
	})
	if !open {
		a.ed = nil
	}
}

func (a *App) saveEditor(e *appEditor) {
	e.saving = true
	m := e.m
	go func() {
		problems, err := a.svc.SaveEdit(m)
		a.later(func() {
			e.saving = false
			switch {
			case err != nil:
				a.toastErr(err)
			case len(problems) > 0:
				for _, f := range []string{"name", "id", "host", "command", "cwd", "env", "instances"} {
					e.touched[f] = true
				}
				e.problems = problems
			default:
				if a.ed == e {
					a.ed = nil
				}
				if m.IsNew {
					a.toast(i18n.T("gui.forms.editor.added", "name", m.Name))
				} else {
					a.toast(i18n.T("gui.forms.editor.saved", "name", m.Name))
				}
			}
		})
	}()
}

func (a *App) editorForm(e *appEditor) {
	c, p := a.c, a.p
	m := &e.m
	ui.Grid(c).ColumnTracks(ui.Fixed(130), ui.Fr(1)).GapX(16).GapY(14).Children(func() {
		// Name
		lbl := a.label(i18n.T("gui.forms.editor.name"))
		ui.Column(c).Children(func() {
			in := ui.TextInput(c.Key("f-name"), &m.Name).LabelledBy(lbl).Invalid(a.fieldErr(e, "name") != "")
			if m.IsNew {
				in.AutoFocus()
			}
			if in.Changed() {
				e.touched["name"] = true
			}
			a.errText(a.fieldErr(e, "name"))
		})
		// ID
		lbl = a.label(i18n.T("gui.forms.editor.id"))
		ui.Column(c).Children(func() {
			id := ui.TextInput(c.Key("f-id"), &m.ID).LabelledBy(lbl).Font("monospace").Width(240).Disabled(!m.IsNew).Invalid(a.fieldErr(e, "id") != "")
			if id.Changed() {
				e.touched["id"] = true
			}
			helpKey := "gui.forms.editor.idHelp"
			if !m.IsNew {
				helpKey = "gui.forms.editor.idHelpFixed"
			}
			shown := m.ID
			if shown == "" {
				shown = i18n.T("gui.forms.editor.idPlaceholder")
			}
			a.help(i18n.T(helpKey, "id", shown))
			a.errText(a.fieldErr(e, "id"))
		})
		// Host
		lbl = a.label(i18n.T("gui.forms.editor.host"))
		ui.Column(c).Children(func() {
			labels := make([]string, 0, len(a.st.Hosts))
			cur := ""
			for _, h := range a.st.Hosts {
				l := h.ID + " (" + h.SSH + ")"
				labels = append(labels, l)
				if h.ID == m.Host {
					cur = l
				}
			}
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				if ui.Select(c.Key("f-host"), &cur, labels).LabelledBy(lbl).Width(300).Changed() {
					m.Host = a.st.Hosts[indexOf(labels, cur)].ID
					e.touched["host"] = true
				}
				if l := ui.ButtonBase(c.Key("add-host")).Label(i18n.T("gui.forms.editor.addHost")).Padding(2, 4).Radius(5).TextColor(p.accent); true {
					l.Children(func() { ui.Text(c, i18n.T("gui.forms.editor.addHost")).FontSize(12.5) })
					if l.Clicked() {
						a.openHost("")
					}
				}
			})
			a.errText(a.fieldErr(e, "host"))
		})
		// Command
		a.label(i18n.T("gui.forms.editor.command"))
		ui.Column(c).Children(func() {
			a.argsBox(e)
			a.help(i18n.T("gui.forms.editor.commandHelp"))
			a.errText(a.fieldErr(e, "command"))
		})
		// Working folder
		lbl = a.label(i18n.T("gui.forms.editor.cwd"))
		ui.Column(c).Children(func() {
			cwd := ui.TextInput(c.Key("f-cwd"), &m.Cwd).LabelledBy(lbl).Font("monospace").Placeholder(i18n.T("gui.forms.editor.cwdPlaceholder")).Invalid(a.fieldErr(e, "cwd") != "")
			if cwd.Changed() {
				e.touched["cwd"] = true
			}
			a.errText(a.fieldErr(e, "cwd"))
		})
		// Environment
		a.label(i18n.T("gui.forms.editor.env"))
		ui.Column(c).Children(func() {
			a.envBox(e)
			a.help(i18n.T("gui.forms.editor.envHelp"))
			a.errText(a.fieldErr(e, "env"))
		})
		// Instances
		a.label(i18n.T("gui.forms.editor.instances"))
		ui.Column(c).Children(func() {
			ui.RadioGroup(c, func() {
				ui.Radio(c, &m.Instances, "single", i18n.T("gui.forms.editor.instSingle"))
				ui.Radio(c, &m.Instances, "multiple", i18n.T("gui.forms.editor.instMultiple"))
			}).Label(i18n.T("gui.forms.editor.instances")).Gap(6).Margin(6, 0, 0)
			a.errText(a.fieldErr(e, "instances"))
		})
		// Preview
		a.label(i18n.T("gui.forms.editor.remote"))
		ui.Column(c).Children(func() {
			ui.Column(c).Padding(10, 12).Radius(10).Background(p.codeBg).Gap(2).Selectable().Children(func() {
				ui.Text(c, i18n.T("gui.forms.editor.startsLocally")).FontSize(10).FontWeight(600).TextColor(p.codeLabel)
				ui.Text(c, e.preview.Local).Font("monospace").FontSize(11).TextColor(p.codeText)
				ui.Text(c, i18n.T("gui.forms.editor.runsOnHost")).FontSize(10).FontWeight(600).TextColor(p.codeLabel).Margin(6, 0, 0)
				ui.Text(c, e.preview.Remote).Font("monospace").FontSize(11).TextColor(p.codeText)
			})
			a.errText(a.fieldErr(e, "general"))
		})
	})
}

// argsBox shows the command as one chip per argument followed by an input
// that turns what is typed into more chips.
func (a *App) argsBox(e *appEditor) {
	c, p := a.c, a.p
	box := ui.Row(c).Wrap().Gap(6).Padding(6).Radius(10).Background(p.field).AlignItems(ui.Center)
	box.Border(1, p.fieldLine)
	if a.fieldErr(e, "command") != "" {
		box.BorderColor(p.err.fg)
	}
	box.Children(func() {
		remove, edit := -1, -1
		for i, arg := range e.m.Command {
			i, arg := i, arg
			bg, fg := p.chip, p.chipText
			if i == 0 {
				bg, fg = p.info.bg, p.info.fg
			}
			shown := arg
			if shown == "" {
				shown = `""`
			}
			ui.Row(c.Key(fmt.Sprint("arg", i))).Height(26).Radius(7).Background(bg).AlignItems(ui.Center).Children(func() {
				t := ui.ButtonBase(c.Key("t")).Label(i18n.T("gui.forms.editor.editArg", "arg", shown)).Height(26).Padding(0, 4, 0, 10).Radius(7, 0, 0, 7).AlignItems(ui.Center)
				t.Children(func() { ui.Text(c, shown).Font("monospace").FontSize(12).TextColor(fg).SingleLine() })
				if t.Clicked() {
					edit = i
				}
				x := ui.ButtonBase(c.Key("x")).Label(i18n.T("gui.forms.editor.removeArg", "arg", shown)).Size(22, 26).Center().Radius(0, 7, 7, 0).TextColor(p.muted)
				x.Children(func() { ui.Icon(c, icX).FontSize(10) })
				if x.Clicked() {
					remove = i
				}
			})
		}
		in := ui.TextInputBase(c.Key("arg-input"), &e.draft).Label(i18n.T("gui.forms.editor.addArg")).Font("monospace").FontSize(12).
			Height(26).Padding(0, 8).MinWidth(140).Grow(1).Placeholder(i18n.T("gui.forms.editor.argPlaceholder"))
		in.OnTextKey(func(k ui.TextKey) bool {
			switch {
			case k.Key == ui.KeyEnter && k.Mods == 0:
				if strings.TrimSpace(k.Text()) != "" {
					rest := e.absorb(k.Text(), true)
					k.SetText(rest)
				}
				return true
			case k.Key == ui.KeyBackspace && k.Mods == 0 && k.Text() == "" && len(e.m.Command) > 0:
				e.m.Command = e.m.Command[:len(e.m.Command)-1]
				e.touched["command"] = true
				return true
			}
			return false
		})
		if in.Changed() && e.draft != "" {
			if rest := e.absorb(e.draft, false); rest != e.draft {
				e.draft = rest
			}
		}
		switch {
		case remove >= 0:
			e.m.Command = append(e.m.Command[:remove:remove], e.m.Command[remove+1:]...)
			e.touched["command"] = true
		case edit >= 0:
			// Click to edit: the argument goes back into the input.
			if e.draft != "" {
				e.absorb(e.draft+" ", true)
			}
			arg := e.m.Command[edit]
			e.m.Command = append(e.m.Command[:edit:edit], e.m.Command[edit+1:]...)
			e.draft = quoteArg(arg)
		}
	})
}

func quoteArg(s string) string {
	if s == "" {
		return `""`
	}
	if strings.ContainsAny(s, " \t\"'\\") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	return s
}

func (a *App) envBox(e *appEditor) {
	c, p := a.c, a.p
	ui.Column(c).Radius(10).Background(p.field).Border(1, p.fieldLine).Clip().Children(func() {
		remove := -1
		for i := range e.m.Env {
			i := i
			bad := ""
			for _, pr := range e.problems {
				if pr.Field == "env."+itoa(i) {
					bad += pr.Message + " "
				}
			}
			row := ui.Column(c.Key(fmt.Sprint("env", i)))
			if bad != "" {
				row.Background(p.badField)
			}
			row.Children(func() {
				ui.Grid(c).ColumnTracks(ui.Fixed(200), ui.Fr(1), ui.Fixed(34)).Children(func() {
					if ui.TextInputBase(c.Key("n"), &e.m.Env[i].Name).Label(i18n.T("gui.forms.editor.varName")).Font("monospace").FontSize(12).Height(30).Padding(0, 10).Placeholder("NAME").Changed() {
						e.touched["env"] = true
					}
					if ui.TextInputBase(c.Key("v"), &e.m.Env[i].Value).Label(i18n.T("gui.forms.editor.varValue")).Font("monospace").FontSize(12).Height(30).Padding(0, 10).Placeholder(i18n.T("gui.forms.editor.varValuePlaceholder")).Changed() {
						e.touched["env"] = true
					}
					x := ui.ButtonBase(c.Key("x")).Label(i18n.T("gui.forms.editor.removeVar")).Height(30).Center().TextColor(p.muted)
					x.Children(func() { ui.Icon(c, icX).FontSize(11) })
					if x.Clicked() {
						remove = i
					}
				})
				if bad != "" {
					ui.Text(c, strings.TrimSpace(bad)).FontSize(11.5).TextColor(p.dangerText).Padding(6, 10)
				}
				ui.Divider(c).Background(p.hair)
			})
		}
		if remove >= 0 {
			e.m.Env = append(e.m.Env[:remove:remove], e.m.Env[remove+1:]...)
		}
		ui.Row(c).Padding(6).Children(func() {
			b := ui.Button(c, "").Label(i18n.T("gui.forms.editor.addVar")).Height(26).Padding(0, 10).Radius(7).Gap(4).FontSize(12)
			b.Children(func() { ui.Icon(c, icPlus).FontSize(11); ui.Text(c, i18n.T("gui.forms.editor.varButton")) })
			if b.Clicked() {
				e.m.Env = append(e.m.Env, launcher.EnvVar{})
				e.touched["env"] = true
			}
		})
	})
}
