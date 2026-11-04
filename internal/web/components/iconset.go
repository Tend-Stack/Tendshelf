package components

// iconShapes holds the inner SVG markup of Tendshelf's icon set (design-system/components/Icon/README.md).
// The strings are constants in this file, never user input, so they are rendered as raw markup.
var iconShapes = map[string]string{
	"panel-left":   `<rect x="3.5" y="4.5" width="17" height="15" rx="2.5"/><path d="M9.5 4.5v15"/>`,
	"home":         `<path d="M3.5 10.5 12 3.5l8.5 7V20a1 1 0 0 1-1 1H15v-6H9v6H4.5a1 1 0 0 1-1-1z"/>`,
	"library":      `<path d="M4 4h4v16H4zM10 4h4v16h-4zM15.8 5.2l3.4-.9 3.4 14.6-3.4.9z"/>`,
	"search":       `<circle cx="11" cy="11" r="7"/><path d="m20 20-3.5-3.5"/>`,
	"download":     `<path d="M12 4v11M7 10l5 5 5-5M5 20h14"/>`,
	"trash":        `<path d="M5 7h14M10 7V5h4v2M7 7l1 13h8l1-13"/>`,
	"profile":      `<circle cx="12" cy="8" r="4"/><path d="M4 21c1.5-4 4.5-6 8-6s6.5 2 8 6"/>`,
	"play":         `<path d="M8 5.5v13a1 1 0 0 0 1.5.86l10.5-6.5a1 1 0 0 0 0-1.72L9.5 4.64A1 1 0 0 0 8 5.5z"/>`,
	"pause":        `<rect x="6" y="5" width="4" height="14" rx="1"/><rect x="14" y="5" width="4" height="14" rx="1"/>`,
	"skip-back":    `<path d="M4.5 12a7.5 7.5 0 1 0 2.4-5.5"/><path d="M4.5 3.5v4h4"/>`,
	"skip-forward": `<path d="M19.5 12a7.5 7.5 0 1 1-2.4-5.5"/><path d="M19.5 3.5v4h-4"/>`,
	"prev-chapter": `<path d="M6 5v14"/><path d="M18.5 6.5v11L10 12z"/>`,
	"next-chapter": `<path d="M18 5v14"/><path d="M5.5 6.5v11L14 12z"/>`,
	"sleep":        `<path d="M19.5 14.5A8 8 0 0 1 9.5 4.5a8 8 0 1 0 10 10z"/>`,
	"bookmark":     `<path d="M6.5 4h11v16.5L12 16.8l-5.5 3.7z"/>`,
	"chapters":     `<path d="M9 6h11M9 12h11M9 18h11M4.5 6h.01M4.5 12h.01M4.5 18h.01"/>`,
	"speaker":      `<path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z"/><path d="M15.5 9a4.2 4.2 0 0 1 0 6M18 6.5a8 8 0 0 1 0 11"/>`,
	"output":       `<path d="M4 9.5h3.5L12 5.5v13l-4.5-4H4z"/><path d="M15.5 9a4.2 4.2 0 0 1 0 6M18 6.5a8 8 0 0 1 0 11"/>`,
	"share":        `<path d="M12 3v12M7 8l5-5 5 5M5 13v6a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1v-6"/>`,
	"invite":       `<circle cx="10" cy="8" r="4"/><path d="M3 21c1.4-4 4-6 7-6s5.6 2 7 6M19 8v6M16 11h6"/>`,
	"edit":         `<path d="M4 20h4L19 9l-4-4L4 16z"/>`,
	"filter":       `<path d="M4 6h16M7 12h10M10 18h4"/>`,
	"grid":         `<path d="M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z"/>`,
	"check":        `<path d="m5 12.5 4.5 4.5L19 7.5"/>`,
	"plus":         `<path d="M12 5v14M5 12h14"/>`,
	"close":        `<path d="M6 6l12 12M18 6 6 18"/>`,
	"alert":        `<path d="M12 4 2.5 20h19z"/><path d="M12 10v4M12 17h.01"/>`,
	"folder":       `<path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`,
	"expand":       `<path d="M14 4h6v6M10 20H4v-6M20 4l-7 7M4 20l7-7"/>`,
	"link":         `<path d="M10 14a4 4 0 0 0 5.7 0l3-3a4 4 0 0 0-5.7-5.7l-1 1M14 10a4 4 0 0 0-5.7 0l-3 3a4 4 0 0 0 5.7 5.7l1-1"/>`,
	"lock":         `<rect x="5" y="10" width="14" height="10" rx="2"/><path d="M8 10V7a4 4 0 0 1 8 0v3"/>`,
	"phone":        `<rect x="7" y="3" width="10" height="18" rx="2"/><path d="M11 18h2"/>`,
	"laptop":       `<path d="M4 6h16v10H4zM2 19h20"/>`,
	"mail":         `<rect x="3" y="5" width="18" height="14" rx="2"/><path d="m4 7 8 6 8-6"/>`,
	"headphones":   `<path d="M4 15v-3a8 8 0 0 1 16 0v3"/><rect x="3.5" y="14" width="4" height="6.5" rx="1.5"/><rect x="16.5" y="14" width="4" height="6.5" rx="1.5"/>`,
	"shield":       `<path d="M12 3 4 6v6c0 4.4 3.4 8 8 9 4.6-1 8-4.6 8-9V6z"/>`,
	"sun":          `<circle cx="12" cy="12" r="4"/><path d="M12 3v2M12 19v2M3 12h2M19 12h2M5.6 5.6 7 7M17 17l1.4 1.4M5.6 18.4 7 17M17 7l1.4-1.4"/>`,
	"sunrise":      `<path d="M4 18h16M7.5 18a4.5 4.5 0 0 1 9 0M12 6v4M5.6 11.6l1.4 1.4M18.4 11.6 17 13M9.5 8 12 5.5 14.5 8"/>`,
	"sunset":       `<path d="M4 18h16M7.5 18a4.5 4.5 0 0 1 9 0M12 5.5V10M5.6 11.6l1.4 1.4M18.4 11.6 17 13M9.5 7.5 12 10l2.5-2.5"/>`,
}

// IconNames lists the available icon names.
func IconNames() []string {
	names := make([]string, 0, len(iconShapes))
	for n := range iconShapes {
		names = append(names, n)
	}
	return names
}
