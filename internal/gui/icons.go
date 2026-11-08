package gui

import "github.com/egoist/mygo/ui"

func svg(body string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`))
}

var (
	icApps = svg(`<rect x="4" y="4" width="7" height="7" rx="1.6"/><rect x="13" y="4" width="7" height="7" rx="1.6"/><rect x="4" y="13" width="7" height="7" rx="1.6"/><rect x="13" y="13" width="7" height="7" rx="1.6"/>`)
	icRun  = svg(`<circle cx="12" cy="12" r="8.5"/><path d="M10 8.8l5 3.2-5 3.2z"/>`)
	icDisc = svg(`<circle cx="11" cy="11" r="6.5"/><path d="M20 20l-4.2-4.2"/><path d="M11 8v6M8 11h6"/>`)
	icSet  = svg(`<path d="M4 7h9M17 7h3M4 17h3M11 17h9"/><circle cx="15" cy="7" r="2"/><circle cx="9" cy="17" r="2"/>`)
	icDots = svg(`<g fill="currentColor" stroke="none"><circle cx="6" cy="12" r="1.7"/><circle cx="12" cy="12" r="1.7"/><circle cx="18" cy="12" r="1.7"/></g>`)
	icPlus = svg(`<path d="M12 5v14M5 12h14"/>`)
	icPin  = svg(`<path d="M9 4h6l-1 5 3 3H7l3-3z"/><path d="M12 12v8"/>`)
	icHost = svg(`<rect x="4" y="5" width="16" height="6" rx="1.8"/><rect x="4" y="13" width="16" height="6" rx="1.8"/><path d="M8 8h.01M8 16h.01"/>`)
	icX    = svg(`<path d="M7 7l10 10M17 7L7 17"/>`)
)
