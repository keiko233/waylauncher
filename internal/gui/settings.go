package gui

import (
	"sort"

	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/i18n"
	"github.com/keiko233/waylauncher/internal/profile"
)

func (a *App) saveSettings(s profile.Settings) {
	a.async(func() (string, error) { return "", a.svc.SaveSettings(s) }, nil)
}

// group is a titled card of rows.
func (a *App) group(title string, rows func()) {
	ui.Text(a.c, title).FontSize(12).FontWeight(600).TextColor(a.p.muted).Margin(18, 4, 8)
	ui.Column(a.c).Radius(12).Background(a.p.card).Padding(4, 18).Dividers(1, a.p.hair).Gap(1).Children(rows)
}

// setRow is a label and description with a control at the end (or below).
func (a *App) setRow(title, desc string, control func(), below bool) {
	ui.Row(a.c).Gap(16).Padding(14, 0).AlignItems(ui.Center).Children(func() {
		ui.Column(a.c).Grow(1).MinWidth(0).Gap(3).Children(func() {
			ui.Text(a.c, title).FontSize(13.5).FontWeight(600)
			if desc != "" {
				ui.Text(a.c, desc).FontSize(12).TextColor(a.p.muted).Selectable()
			}
			if below {
				control()
			}
		})
		if !below && control != nil {
			ui.Row(a.c).Shrink(0).Children(control)
		}
	})
}

func (a *App) pageSettings() {
	c, p := a.c, a.p
	s := a.st.Settings
	a.header(i18n.T("gui.forms.settings.title"), i18n.T("gui.forms.settings.sub"), nil)

	themes := []string{"system", "light", "dark"}
	themeIdx := indexOf(themes, s.Theme)
	glassLabels := []string{i18n.T("gui.forms.settings.followSystem"), i18n.T("gui.forms.settings.glassClear"), i18n.T("gui.forms.settings.glassTinted")}
	glasses := []string{"system", "clear", "tinted"}
	glassSel := glassLabels[indexOf(glasses, s.Glass)]
	langs := []string{i18n.SettingSystem}
	langLabels := []string{i18n.T("settings.language.system")}
	for _, t := range i18n.Supported {
		langs = append(langs, string(t))
		langLabels = append(langLabels, t.Native())
	}
	langSel := langLabels[indexOf(langs, s.Language)]
	keeps := []int{5, 10, 20, 50, 100}
	found := false
	for _, k := range keeps {
		found = found || k == s.LogsKeepPerApp
	}
	if !found {
		keeps = append(keeps, s.LogsKeepPerApp)
		sort.Ints(keeps)
	}
	keepLabels := make([]string, len(keeps))
	keepSel := ""
	for i, k := range keeps {
		keepLabels[i] = i18n.T("gui.forms.settings.keepLast", "n", k)
		if k == s.LogsKeepPerApp {
			keepSel = keepLabels[i]
		}
	}

	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 16, 18).Children(func() {
		ui.Row(c).Gap(18).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				a.group(i18n.T("gui.forms.settings.appearance"), func() {
					a.setRow(i18n.T("settings.language.title"), i18n.T("settings.language.help"), func() {
						if ui.Select(c, &langSel, langLabels).Label(i18n.T("settings.language.title")).Width(140).Changed() {
							s.Language = langs[indexOfS(langLabels, langSel)]
							a.saveSettings(s)
						}
					}, false)
					a.setRow(i18n.T("gui.forms.settings.theme"), i18n.T("gui.forms.settings.themeHelp"), func() {
						if ui.Segmented(c, &themeIdx, i18n.T("gui.forms.settings.followSystem"), i18n.T("gui.forms.settings.light"), i18n.T("gui.forms.settings.dark")).Label(i18n.T("gui.forms.settings.theme")).Changed() {
							s.Theme = themes[themeIdx]
							a.saveSettings(s)
						}
					}, false)
					a.setRow(i18n.T("gui.forms.settings.glass"), i18n.T("gui.forms.settings.glassHelp"), func() {
						if ui.Select(c, &glassSel, glassLabels).Label(i18n.T("gui.forms.settings.glass")).Width(140).Changed() {
							s.Glass = glasses[indexOfS(glassLabels, glassSel)]
							a.saveSettings(s)
						}
					}, false)
					a.setRow(i18n.T("gui.forms.settings.menuBar"), i18n.T("gui.forms.settings.menuBarHelp"), func() {
						if ui.Switch(c, &s.ShowInMenuBar).Label(i18n.T("gui.forms.settings.menuBar")).Changed() {
							a.saveSettings(s)
						}
					}, false)
					a.setRow(i18n.T("gui.forms.settings.dock"), i18n.T("gui.forms.settings.dockHelp"), func() {
						if ui.Switch(c, &s.ShowRunningInDock).Label(i18n.T("gui.forms.settings.dock")).Changed() {
							a.saveSettings(s)
						}
					}, false)
				})
				a.group(i18n.T("gui.forms.settings.reopen"), func() {
					a.setRow(i18n.T("gui.forms.settings.single"), i18n.T("gui.forms.settings.singleHelp"), func() {
						a.muted(i18n.T("gui.forms.settings.always"), 12)
					}, false)
					a.setRow(i18n.T("gui.forms.settings.multi"), i18n.T("gui.forms.settings.multiHelp"), func() {
						ui.RadioGroup(c, func() {
							ui.Radio(c, &s.MultiInstanceReopen, "focus-recent", i18n.T("gui.forms.settings.focusRecent"))
							ui.Radio(c, &s.MultiInstanceReopen, "new", i18n.T("gui.forms.settings.startAnother"))
						}).Label(i18n.T("gui.forms.settings.multi")).Gap(6).Margin(6, 0, 0)
						if s.MultiInstanceReopen != a.st.Settings.MultiInstanceReopen {
							a.saveSettings(s)
						}
					}, true)
				})
			})
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				a.group(i18n.T("gui.forms.settings.quitting"), func() {
					a.setRow(i18n.T("gui.forms.settings.stillRunning"), i18n.T("gui.forms.settings.stillRunningHelp"), func() {
						ui.RadioGroup(c, func() {
							ui.Radio(c, &s.QuitBehavior, "immediate", i18n.T("gui.forms.settings.quitNow"))
							ui.Radio(c, &s.QuitBehavior, "ask", i18n.T("gui.forms.settings.quitAsk"))
						}).Label(i18n.T("gui.forms.settings.stillRunning")).Gap(6).Margin(6, 0, 0)
						if s.QuitBehavior != a.st.Settings.QuitBehavior {
							a.saveSettings(s)
						}
					}, true)
				})
				a.group(i18n.T("gui.forms.settings.disconnects"), func() {
					a.setRow(i18n.T("gui.forms.settings.noChild"), i18n.T("gui.forms.settings.noChildHelp"), func() {
						if ui.Switch(c, &s.TreatNoChildProcessesAsNormal).Label(i18n.T("gui.forms.settings.noChildLabel")).Changed() {
							a.saveSettings(s)
						}
					}, false)
					a.setRow(i18n.T("gui.forms.settings.notify"), i18n.T("gui.forms.settings.notifyHelp"), func() {
						if ui.Switch(c, &s.NotifyOnDisconnect).Label(i18n.T("gui.forms.settings.notify")).Changed() {
							a.saveSettings(s)
						}
					}, false)
				})
				a.group(i18n.T("gui.forms.settings.logs"), func() {
					a.setRow(i18n.T("gui.forms.settings.keepLogs"), a.st.LogDir, func() {
						if ui.Select(c, &keepSel, keepLabels).Label(i18n.T("gui.forms.settings.keepLogsLabel")).Width(110).Changed() {
							s.LogsKeepPerApp = keeps[indexOfS(keepLabels, keepSel)]
							a.saveSettings(s)
						}
					}, false)
				})
				a.group(i18n.T("gui.forms.settings.about"), func() {
					ui.Column(c).Padding(14, 0).Gap(3).Children(func() {
						ui.Text(c, "Waylauncher "+a.st.Version).FontSize(13.5).FontWeight(600)
						ui.Text(c, i18n.T("gui.forms.settings.profiles", "path", a.st.ConfigDir+"/profiles.toml")).
							FontSize(12).TextColor(p.muted).Selectable()
					})
				})
			})
		})
	})
}

func indexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return 0
}

func indexOfS(list []string, v string) int { return indexOf(list, v) }
