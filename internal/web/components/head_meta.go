package components

// themeGround mirrors --ground of each theme block in static/css/tokens.css. The browser toolbar
// (theme-color) takes it so the chrome around the page matches the page. A test keeps both in step.
var themeGround = map[string]string{
	"dark":     "#14110e",
	"light":    "#f4eee3",
	"sepia":    "#e6d7bc",
	"contrast": "#000000",
}

// withoutInstall is p without the install links: a share-link page is not part of the installed app.
func withoutInstall(p Page) Page {
	p.NoInstall = true
	return p
}

// ThemeColor is the theme-color of a theme name, for the web app manifest.
func ThemeColor(theme string) string { return themeColor(theme) }

// colorSchemeMeta is the content of <meta name="color-scheme">, mirroring the color-scheme property
// of the theme blocks in tokens.css: light and sepia are light, dark and contrast are dark, and an
// empty theme follows the system.
func colorSchemeMeta(theme string) string {
	switch theme {
	case "":
		return "dark light"
	case "light", "sepia":
		return "light"
	}
	return "dark"
}

// themeColor is the theme-color for a theme name; an unknown name gets the dark ground.
func themeColor(theme string) string {
	if c, ok := themeGround[theme]; ok {
		return c
	}
	return themeGround["dark"]
}
