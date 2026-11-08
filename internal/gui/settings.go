package gui

import (
	"sort"

	"github.com/egoist/mygo/ui"

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
	a.header("Settings", "Changes apply immediately.", nil)

	themes := []string{"system", "light", "dark"}
	themeIdx := indexOf(themes, s.Theme)
	glassLabels := []string{"Follow system", "Clear", "Tinted"}
	glasses := []string{"system", "clear", "tinted"}
	glassSel := glassLabels[indexOf(glasses, s.Glass)]
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
		keepLabels[i] = "Last " + itoa(k)
		if k == s.LogsKeepPerApp {
			keepSel = keepLabels[i]
		}
	}

	ui.Scroll(c).Grow(1).MinHeight(0).Padding(0, 16, 18).Children(func() {
		ui.Row(c).Gap(18).AlignItems(ui.Start).Children(func() {
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				a.group("Appearance", func() {
					a.setRow("Theme", "Applies to the window and its menus.", func() {
						if ui.Segmented(c, &themeIdx, "Follow system", "Light", "Dark").Label("Theme").Changed() {
							s.Theme = themes[themeIdx]
							a.saveSettings(s)
						}
					}, false)
					a.setRow("Glass", "The window material. “Follow system” uses the regular glass: apps can’t read the Liquid Glass slider of System Settings.", func() {
						if ui.Select(c, &glassSel, glassLabels).Label("Glass").Width(140).Changed() {
							s.Glass = glasses[indexOfS(glassLabels, glassSel)]
							a.saveSettings(s)
						}
					}, false)
					a.setRow("Show in menu bar", "Pinned and running apps, one click away.", func() {
						if ui.Switch(c, &s.ShowInMenuBar).Label("Show in menu bar").Changed() {
							a.saveSettings(s)
						}
					}, false)
					a.setRow("Show running Linux apps in the Dock", "Each running app gets its own Dock icon with a running dot. Clicking it brings Cocoa-Way forward; Quit from its menu stops the app. The windows still belong to Cocoa-Way, so its own icon stays.", func() {
						if ui.Switch(c, &s.ShowRunningInDock).Label("Show running Linux apps in the Dock").Changed() {
							a.saveSettings(s)
						}
					}, false)
				})
				a.group("Opening an app that is already running", func() {
					a.setRow("Single-instance apps", "Focus its window. Until Cocoa-Way can report and activate windows, Waylauncher brings Cocoa-Way forward instead.", func() {
						a.muted("Always", 12)
					}, false)
					a.setRow("Multiple-instance apps", "“New window” on an application always starts another instance.", func() {
						ui.RadioGroup(c, func() {
							ui.Radio(c, &s.MultiInstanceReopen, "focus-recent", "Focus the most recent window")
							ui.Radio(c, &s.MultiInstanceReopen, "new", "Start another instance")
						}).Label("Multiple-instance apps").Gap(6).Margin(6, 0, 0)
						if s.MultiInstanceReopen != a.st.Settings.MultiInstanceReopen {
							a.saveSettings(s)
						}
					}, true)
				})
			})
			ui.Column(c).Grow(1).Basis(0).MinWidth(0).Children(func() {
				a.group("Quitting Waylauncher", func() {
					a.setRow("When apps are still running", "Quitting closes every running Linux app: Waypipe sessions can’t be kept or reattached. Unsaved work in them is lost. Cocoa-Way keeps running.", func() {
						ui.RadioGroup(c, func() {
							ui.Radio(c, &s.QuitBehavior, "immediate", "Quit right away (default)")
							ui.Radio(c, &s.QuitBehavior, "ask", "Ask first")
						}).Label("When apps are still running").Gap(6).Margin(6, 0, 0)
						if s.QuitBehavior != a.st.Settings.QuitBehavior {
							a.saveSettings(s)
						}
					}, true)
				})
				a.group("Disconnects", func() {
					a.setRow("Count “No child processes” as a normal close", "waypipe-darwin can exit with code 1 when a window closes normally. With this on, that shows as “Exited normally” with no alert.", func() {
						if ui.Switch(c, &s.TreatNoChildProcessesAsNormal).Label("Count No child processes as a normal close").Changed() {
							a.saveSettings(s)
						}
					}, false)
					a.setRow("Notify when an app disconnects", "Apps are never restarted automatically.", func() {
						if ui.Switch(c, &s.NotifyOnDisconnect).Label("Notify when an app disconnects").Changed() {
							a.saveSettings(s)
						}
					}, false)
				})
				a.group("Logs", func() {
					a.setRow("Keep logs per app", a.st.LogDir, func() {
						if ui.Select(c, &keepSel, keepLabels).Label("Logs to keep per app").Width(110).Changed() {
							s.LogsKeepPerApp = keeps[indexOfS(keepLabels, keepSel)]
							a.saveSettings(s)
						}
					}, false)
				})
				a.group("About", func() {
					ui.Column(c).Padding(14, 0).Gap(3).Children(func() {
						ui.Text(c, "Waylauncher "+a.st.Version).FontSize(13.5).FontWeight(600)
						ui.Text(c, "Profiles: "+a.st.ConfigDir+"/profiles.toml\nEdits keep a backup of the previous file. Passwords and keys are never stored.").
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
