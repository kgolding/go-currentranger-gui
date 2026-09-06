package main

import (
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

// darkTheme overrides a handful of colors to match the Python app's
// Catppuccin-style dark palette; everything else falls back to Fyne's
// built-in dark theme.
type darkTheme struct{}

var (
	themeBG      = color.NRGBA{0x1e, 0x1e, 0x2e, 0xFF}
	themeFG      = color.NRGBA{0xcd, 0xd6, 0xf4, 0xFF}
	themeAccent  = color.NRGBA{0x89, 0xb4, 0xfa, 0xFF}
	themeAccent2 = color.NRGBA{0xa6, 0xe3, 0xa1, 0xFF}
	themeButton  = color.NRGBA{0x31, 0x32, 0x44, 0xFF}
)

func (d darkTheme) Color(name fyne.ThemeColorName, variant fyne.ThemeVariant) color.Color {
	switch name {
	case theme.ColorNameBackground:
		return themeBG
	case theme.ColorNameForeground:
		return themeFG
	case theme.ColorNamePrimary:
		return themeAccent
	case theme.ColorNameButton, theme.ColorNameInputBackground:
		return themeButton
	case theme.ColorNameSuccess:
		return themeAccent2
	}
	return theme.DefaultTheme().Color(name, theme.VariantDark)
}

func (d darkTheme) Font(style fyne.TextStyle) fyne.Resource {
	return theme.DefaultTheme().Font(style)
}

func (d darkTheme) Icon(name fyne.ThemeIconName) fyne.Resource {
	return theme.DefaultTheme().Icon(name)
}

func (d darkTheme) Size(name fyne.ThemeSizeName) float32 {
	return theme.DefaultTheme().Size(name)
}
