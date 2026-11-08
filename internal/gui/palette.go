package gui

import (
	"github.com/egoist/mygo/ui"

	"github.com/keiko233/waylauncher/internal/launcher"
)

// tone is a status colour set: pill background, text and dot.
type tone struct{ bg, fg ui.Color }

// palette holds the colours of one appearance. Container panels have no
// stroke (fill only); application cards keep a 1px border, cardLine.
type palette struct {
	dark bool

	window, sidebarSolid, panel, card, cardLine ui.Color
	hair, hairSoft, hover                       ui.Color
	text, text2, muted, chip, chipText          ui.Color
	field, fieldLine                            ui.Color
	accent, accentText, navOn, navOnText        ui.Color
	danger, dangerText                          ui.Color
	note, codeBg, codeText, codeLabel           ui.Color
	glassFill, glassLine, scrim                 ui.Color
	rowSel, badField                            ui.Color

	ok, warn, err, idle, info tone

	icApps, icRun, icDisc, icSet ui.Color
}

func newPalette(dark bool) *palette {
	if dark {
		return &palette{
			dark:   true,
			window: ui.Hex("#141519"), sidebarSolid: ui.Hex("#1E2026"), panel: ui.Hex("#1D1F25"),
			card: ui.Hex("#24262C"), cardLine: ui.RGBA(255, 255, 255, 0.07),
			hair: ui.RGBA(255, 255, 255, 0.08), hairSoft: ui.RGBA(255, 255, 255, 0.06), hover: ui.RGBA(255, 255, 255, 0.07),
			text: ui.Hex("#F2F2F5"), text2: ui.Hex("#D6D7DD"), muted: ui.Hex("#A6A7B0"),
			chip: ui.RGBA(255, 255, 255, 0.08), chipText: ui.Hex("#D6D7DD"),
			field: ui.Hex("#2E3038"), fieldLine: ui.RGBA(255, 255, 255, 0.14),
			accent: ui.Hex("#2F7BEF"), accentText: ui.Hex("#FFFFFF"),
			navOn: ui.RGBA(64, 140, 255, 0.24), navOnText: ui.Hex("#CFE2FF"),
			danger: ui.Hex("#D8392C"), dangerText: ui.Hex("#FF8A82"),
			note: ui.RGBA(255, 255, 255, 0.05), codeBg: ui.Hex("#101216"), codeText: ui.Hex("#D7DAE0"), codeLabel: ui.Hex("#8B8F9A"),
			glassFill: ui.RGBA(255, 255, 255, 0.09), glassLine: ui.RGBA(255, 255, 255, 0.12), scrim: ui.RGBA(0, 0, 0, 0.5),
			rowSel: ui.RGBA(64, 140, 255, 0.12), badField: ui.RGBA(255, 95, 87, 0.1),
			ok:     tone{ui.RGBA(60, 203, 110, 0.16), ui.Hex("#5BDB8A")},
			warn:   tone{ui.RGBA(255, 190, 70, 0.16), ui.Hex("#F2B350")},
			err:    tone{ui.RGBA(255, 95, 87, 0.16), ui.Hex("#FF8A82")},
			idle:   tone{ui.RGBA(255, 255, 255, 0.08), ui.Hex("#B4B5BD")},
			info:   tone{ui.RGBA(64, 140, 255, 0.18), ui.Hex("#9CC4FF")},
			icApps: ui.Hex("#5C9DFF"), icRun: ui.Hex("#3CCB6E"), icDisc: ui.Hex("#BF8CFF"), icSet: ui.Hex("#9A9AA6"),
		}
	}
	return &palette{
		window: ui.Hex("#F4F5F8"), sidebarSolid: ui.Hex("#ECEEF3"), panel: ui.Hex("#FFFFFF"),
		card: ui.Hex("#FCFCFD"), cardLine: ui.Hex("#E6E7EB"),
		hair: ui.Hex("#ECEDF0"), hairSoft: ui.Hex("#F0F1F3"), hover: ui.RGBA(0, 0, 0, 0.05),
		text: ui.Hex("#1C1C1E"), text2: ui.Hex("#3A3A40"), muted: ui.Hex("#5B5B63"),
		chip: ui.Hex("#F1F2F4"), chipText: ui.Hex("#3A3A40"),
		field: ui.Hex("#FFFFFF"), fieldLine: ui.Hex("#D6D8DD"),
		accent: ui.Hex("#0060DF"), accentText: ui.Hex("#FFFFFF"),
		navOn: ui.RGBA(0, 96, 223, 0.12), navOnText: ui.Hex("#0049AA"),
		danger: ui.Hex("#C4271B"), dangerText: ui.Hex("#B42318"),
		note: ui.Hex("#F4F6FA"), codeBg: ui.Hex("#16181D"), codeText: ui.Hex("#D7DAE0"), codeLabel: ui.Hex("#8B8F9A"),
		glassFill: ui.RGBA(255, 255, 255, 0.72), glassLine: ui.RGBA(0, 0, 0, 0.08), scrim: ui.RGBA(20, 24, 36, 0.28),
		rowSel: ui.Hex("#F2F7FF"), badField: ui.Hex("#FFF6F5"),
		ok:     tone{ui.Hex("#E3F4E8"), ui.Hex("#1E6B38")},
		warn:   tone{ui.Hex("#FFF1D6"), ui.Hex("#8A5300")},
		err:    tone{ui.Hex("#FDE8E6"), ui.Hex("#B42318")},
		idle:   tone{ui.Hex("#EEEEF0"), ui.Hex("#55555C")},
		info:   tone{ui.Hex("#E8F0FD"), ui.Hex("#0049AA")},
		icApps: ui.Hex("#0A64D8"), icRun: ui.Hex("#1E9E4A"), icDisc: ui.Hex("#8E4EC6"), icSet: ui.Hex("#6E6E78"),
	}
}

func (p *palette) tone(k launcher.Kind) tone {
	switch k {
	case launcher.KindOK:
		return p.ok
	case launcher.KindWarn:
		return p.warn
	case launcher.KindErr:
		return p.err
	}
	return p.idle
}

// theme derives the toolkit theme (used by the stock widgets) from the
// system's, keeping its accent and accessibility settings.
func (p *palette) theme(sys *ui.Theme) *ui.Theme {
	t := *sys
	t.Background = p.window
	t.Surface = p.field
	if p.dark {
		t.SurfaceHover = ui.Hex("#383B45")
		t.SurfacePressed = ui.Hex("#42454F")
	} else {
		t.SurfaceHover = ui.Hex("#F4F5F7")
		t.SurfacePressed = ui.Hex("#E9EAEE")
	}
	t.Border = p.fieldLine
	t.Text, t.TextMuted = p.text, p.muted
	t.Danger = p.danger
	t.Radius = 8
	t.FontSize = 13
	return &t
}
