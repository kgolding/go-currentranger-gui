package main

import "fyne.io/fyne/v2"

// sidebarWidth is the stats sidebar's fixed width — wide enough for its
// label/value pairs to sit side by side rather than stacked.
const sidebarWidth float32 = 260

// mainLayout arranges the toolbar (top), stats sidebar (right, fixed
// width) and chart (center) — like container.NewBorder, but pins the
// sidebar to sidebarWidth instead of letting Border derive it from the
// sidebar's own MinSize.
type mainLayout struct{}

// Layout expects objects in a fixed order: toolbar, statsPanel, chart.
func (l *mainLayout) Layout(objects []fyne.CanvasObject, size fyne.Size) {
	toolbar, stats, chart := objects[0], objects[1], objects[2]

	topH := toolbar.MinSize().Height
	midH := size.Height - topH
	if midH < 0 {
		midH = 0
	}

	toolbar.Move(fyne.NewPos(0, 0))
	toolbar.Resize(fyne.NewSize(size.Width, topH))

	stats.Move(fyne.NewPos(size.Width-sidebarWidth, topH))
	stats.Resize(fyne.NewSize(sidebarWidth, midH))

	chartW := size.Width - sidebarWidth
	if chartW < 0 {
		chartW = 0
	}
	chart.Move(fyne.NewPos(0, topH))
	chart.Resize(fyne.NewSize(chartW, midH))
}

func (l *mainLayout) MinSize(objects []fyne.CanvasObject) fyne.Size {
	toolbar, stats, chart := objects[0], objects[1], objects[2]
	w := fyne.Max(toolbar.MinSize().Width, stats.MinSize().Width+chart.MinSize().Width)
	h := toolbar.MinSize().Height + fyne.Max(stats.MinSize().Height, chart.MinSize().Height)
	return fyne.NewSize(w, h)
}
