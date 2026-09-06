package main

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"math"
	"sync"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/widget"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Theme colors, matching the Python dark palette.
var (
	colPlotBG = color.NRGBA{0x18, 0x18, 0x25, 0xFF}
	colGrid   = color.NRGBA{0x45, 0x47, 0x5a, 0xFF}
	colTrace  = color.NRGBA{0x89, 0xdc, 0xeb, 0xFF}
	colAvg    = color.NRGBA{0xf9, 0xe2, 0xaf, 0xFF}
	colRoll   = color.NRGBA{0xe7, 0x4c, 0x3c, 0xFF}
	colSel    = color.NRGBA{0x89, 0xb4, 0xfa, 0x50}
	colFG     = color.NRGBA{0xcd, 0xd6, 0xf4, 0xFF}
)

const (
	padLeft   = 60
	padRight  = 12
	padTop    = 12
	padBottom = 28
)

// ChartWidget is a self-drawn line chart supporting the same interactions
// the Python matplotlib canvas had: click-drag to select a time range,
// and scroll-to-zoom (which also pauses live scrolling, same as Python).
type ChartWidget struct {
	widget.BaseWidget

	mu sync.Mutex

	// Data to render, already restricted to the visible window.
	// plotT/avgT are in plot-relative seconds (0 == left edge of window).
	plotT []float64
	plotC []float64
	avgT  []float64 // independent time base for the smoothed average line
	avgC  []float64
	rollT []float64 // independent time base for the time-windowed rolling average
	rollC []float64

	xMin, xMax float64
	yMin, yMax float64

	hasSelection   bool
	selX0, selX1   float64 // plot-relative seconds
	dragging       bool
	dragStartPlotX float64

	// Callbacks into the app.
	OnSelectionChanged func(x0, x1 float64, active bool)
	OnScrollZoom       func(centerPlotX float64, zoomIn bool)

	raster *canvas.Raster
}

func NewChartWidget() *ChartWidget {
	c := &ChartWidget{xMax: 1, yMax: 1}
	c.ExtendBaseWidget(c)
	c.raster = canvas.NewRaster(c.draw)
	return c
}

func (c *ChartWidget) CreateRenderer() fyne.WidgetRenderer {
	return &chartRenderer{chart: c, raster: c.raster}
}

// SetData updates the plotted series. avgT/avgC and rollT/rollC may each be
// nil to skip that line. xMin/xMax/yMin/yMax define the axis ranges.
func (c *ChartWidget) SetData(plotT, plotC, avgT, avgC, rollT, rollC []float64, xMin, xMax, yMin, yMax float64) {
	c.mu.Lock()
	c.plotT = plotT
	c.plotC = plotC
	c.avgT = avgT
	c.avgC = avgC
	c.rollT = rollT
	c.rollC = rollC
	c.xMin, c.xMax = xMin, xMax
	c.yMin, c.yMax = yMin, yMax
	c.mu.Unlock()
	c.raster.Refresh()
}

// SetSelection sets or clears the selection rectangle (plot-relative x).
func (c *ChartWidget) SetSelection(x0, x1 float64, active bool) {
	c.mu.Lock()
	c.hasSelection = active
	c.selX0, c.selX1 = x0, x1
	c.mu.Unlock()
	c.raster.Refresh()
}

// --- geometry helpers -------------------------------------------------

func (c *ChartWidget) plotRect() (w, h float32) {
	sz := c.Size()
	return sz.Width, sz.Height
}

func (c *ChartWidget) pxToPlotX(px float32) float64 {
	w, _ := c.plotRect()
	innerW := float64(w) - padLeft - padRight
	if innerW <= 0 {
		return c.xMin
	}
	frac := (float64(px) - padLeft) / innerW
	return c.xMin + frac*(c.xMax-c.xMin)
}

// --- mouse interaction --------------------------------------------------

func (c *ChartWidget) Dragged(ev *fyne.DragEvent) {
	c.mu.Lock()
	if !c.dragging {
		c.dragging = true
		startPx := ev.Position.X - ev.Dragged.DX
		c.dragStartPlotX = c.pxToPlotXLocked(startPx)
	}
	curX := c.pxToPlotXLocked(ev.Position.X)
	x0, x1 := c.dragStartPlotX, curX
	if x0 > x1 {
		x0, x1 = x1, x0
	}
	c.selX0, c.selX1 = x0, x1
	c.hasSelection = true
	cb := c.OnSelectionChanged
	c.mu.Unlock()
	c.raster.Refresh()
	if cb != nil {
		cb(x0, x1, true)
	}
}

func (c *ChartWidget) pxToPlotXLocked(px float32) float64 {
	sz := c.Size()
	innerW := float64(sz.Width) - padLeft - padRight
	if innerW <= 0 {
		return c.xMin
	}
	frac := (float64(px) - padLeft) / innerW
	return c.xMin + frac*(c.xMax-c.xMin)
}

func (c *ChartWidget) DragEnd() {
	c.mu.Lock()
	c.dragging = false
	x0, x1 := c.selX0, c.selX1
	tiny := math.Abs(x1-x0) < 0.01*(c.xMax-c.xMin)
	if tiny {
		c.hasSelection = false
	}
	cb := c.OnSelectionChanged
	active := c.hasSelection
	c.mu.Unlock()
	c.raster.Refresh()
	if cb != nil {
		cb(x0, x1, active)
	}
}

func (c *ChartWidget) Tapped(_ *fyne.PointEvent) {
	// A plain tap (no drag) clears any existing selection, mirroring the
	// Python behavior where a near-zero-width drag clears the selection.
	c.mu.Lock()
	c.hasSelection = false
	cb := c.OnSelectionChanged
	c.mu.Unlock()
	c.raster.Refresh()
	if cb != nil {
		cb(0, 0, false)
	}
}

func (c *ChartWidget) Scrolled(ev *fyne.ScrollEvent) {
	centerX := c.pxToPlotX(ev.Position.X)
	zoomIn := ev.Scrolled.DY > 0
	if c.OnScrollZoom != nil {
		c.OnScrollZoom(centerX, zoomIn)
	}
}

// --- rendering ------------------------------------------------------------

func (c *ChartWidget) draw(w, h int) image.Image {
	c.mu.Lock()
	plotT := c.plotT
	plotC := c.plotC
	avgT := c.avgT
	avgC := c.avgC
	rollT := c.rollT
	rollC := c.rollC
	xMin, xMax := c.xMin, c.xMax
	yMin, yMax := c.yMin, c.yMax
	hasSel := c.hasSelection
	selX0, selX1 := c.selX0, c.selX1
	c.mu.Unlock()

	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{colPlotBG}, image.Point{}, draw.Src)

	if w < int(padLeft+padRight)+10 || h < int(padTop+padBottom)+10 {
		return img
	}

	innerX0 := float64(padLeft)
	innerX1 := float64(w) - padRight
	innerY0 := float64(padTop)
	innerY1 := float64(h) - padBottom

	if xMax <= xMin {
		xMax = xMin + 1
	}
	if yMax <= yMin {
		yMax = yMin + 1e-9
	}

	// Values outside [xMin,xMax]/[yMin,yMax] (e.g. a pinned Y scale smaller
	// than the live signal) are railed to the plot edge rather than left to
	// land off-canvas, where setPx would silently drop them and the whole
	// trace would vanish with no visual indication of over-range.
	toPx := func(t float64) float64 {
		px := innerX0 + (t-xMin)/(xMax-xMin)*(innerX1-innerX0)
		return clampF(px, innerX0, innerX1)
	}
	toPy := func(v float64) float64 {
		py := innerY1 - (v-yMin)/(yMax-yMin)*(innerY1-innerY0)
		return clampF(py, innerY0, innerY1)
	}

	// Grid + axis labels.
	const nGridX, nGridY = 6, 5
	for i := 0; i <= nGridX; i++ {
		t := xMin + float64(i)/nGridX*(xMax-xMin)
		x := toPx(t)
		drawVLine(img, x, innerY0, innerY1, colGrid)
		drawText(img, fmt.Sprintf("%.1fs", t), x-12, innerY1+16, colFG)
	}
	for i := 0; i <= nGridY; i++ {
		v := yMin + float64(i)/nGridY*(yMax-yMin)
		y := toPy(v)
		drawHLine(img, y, innerX0, innerX1, colGrid)
		drawText(img, formatMilliamps(v), 2, y+4, colFG)
	}

	// Selection rectangle.
	if hasSel {
		sx0 := toPx(selX0)
		sx1 := toPx(selX1)
		fillRect(img, sx0, innerY0, sx1, innerY1, colSel)
	}

	// Running average (drawn first, underneath the trace).
	if len(avgT) == len(avgC) && len(avgT) > 1 {
		drawPolyline(img, avgT, avgC, toPx, toPy, colAvg)
	}

	// Trace.
	if len(plotT) > 1 {
		drawPolyline(img, plotT, plotC, toPx, toPy, colTrace)
	}

	// Time-windowed rolling average, drawn on top so it stays readable
	// against the noisier trace beneath it.
	if len(rollT) == len(rollC) && len(rollT) > 1 {
		drawPolyline(img, rollT, rollC, toPx, toPy, colRoll)
	}

	return img
}

func drawPolyline(img *image.RGBA, xs, ys []float64, toPx, toPy func(float64) float64, col color.Color) {
	for i := 1; i < len(xs); i++ {
		if math.IsNaN(ys[i-1]) || math.IsNaN(ys[i]) {
			continue
		}
		drawLine(img, toPx(xs[i-1]), toPy(ys[i-1]), toPx(xs[i]), toPy(ys[i]), col)
	}
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// drawLine is a simple Bresenham-ish line rasterizer, sufficient for a
// thin trace at UI refresh rates.
func drawLine(img *image.RGBA, x0, y0, x1, y1 float64, col color.Color) {
	dx := x1 - x0
	dy := y1 - y0
	steps := math.Max(math.Abs(dx), math.Abs(dy))
	if steps < 1 {
		steps = 1
	}
	for i := 0.0; i <= steps; i++ {
		t := i / steps
		x := int(x0 + dx*t)
		y := int(y0 + dy*t)
		setPx(img, x, y, col)
		setPx(img, x, y+1, col) // 2px thick for visibility
	}
}

func setPx(img *image.RGBA, x, y int, col color.Color) {
	b := img.Bounds()
	if x < b.Min.X || x >= b.Max.X || y < b.Min.Y || y >= b.Max.Y {
		return
	}
	img.Set(x, y, col)
}

func drawVLine(img *image.RGBA, x, y0, y1 float64, col color.Color) {
	for y := int(y0); y <= int(y1); y++ {
		setPx(img, int(x), y, col)
	}
}

func drawHLine(img *image.RGBA, y, x0, x1 float64, col color.Color) {
	for x := int(x0); x <= int(x1); x++ {
		setPx(img, x, int(y), col)
	}
}

func fillRect(img *image.RGBA, x0, y0, x1, y1 float64, col color.NRGBA) {
	rect := image.Rect(int(x0), int(y0), int(x1), int(y1))
	draw.Draw(img, rect.Intersect(img.Bounds()), &image.Uniform{col}, image.Point{}, draw.Over)
}

var textFace = basicfont.Face7x13

func drawText(img *image.RGBA, s string, x, y float64, col color.Color) {
	d := &font.Drawer{
		Dst:  img,
		Src:  &image.Uniform{col},
		Face: textFace,
		Dot:  fixed.P(int(x), int(y)),
	}
	d.DrawString(s)
}

// chartRenderer implements fyne.WidgetRenderer for ChartWidget.
type chartRenderer struct {
	chart  *ChartWidget
	raster *canvas.Raster
}

func (r *chartRenderer) Layout(size fyne.Size) {
	r.raster.Resize(size)
}
func (r *chartRenderer) MinSize() fyne.Size           { return fyne.NewSize(400, 300) }
func (r *chartRenderer) Refresh()                     { r.raster.Refresh() }
func (r *chartRenderer) Objects() []fyne.CanvasObject { return []fyne.CanvasObject{r.raster} }
func (r *chartRenderer) Destroy()                     {}
