package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

const (
	updateInterval    = 50 * time.Millisecond
	maxDisplayPoints  = 4000
)

var windowOptions = []string{"5s", "10s", "30s", "60s", "5m", "All"}
var windowSeconds = map[string]float64{"5s": 5, "10s": 10, "30s": 30, "60s": 60, "5m": 300, "All": 0}

var scaleOptions = []string{"Auto", "100 µA", "1 mA", "10 mA", "100 mA", "500 mA", "1 A"}
var scaleAmps = map[string]float64{
	"Auto":   0,
	"100 µA": 100e-6,
	"1 mA":   1e-3,
	"10 mA":  10e-3,
	"100 mA": 100e-3,
	"500 mA": 500e-3,
	"1 A":    1,
}

// App owns all UI state and the live-update loop. It mirrors
// CurrentRangerApp from the Python version.
type App struct {
	win    fyne.Window
	reader *SerialReader
	chart  *ChartWidget

	timeWindow float64
	yScale     float64 // 0 = auto-range; otherwise a fixed full-scale amps value
	paused     bool
	pausedTs   []float64
	pausedCur  []float64
	plotT0     float64 // absolute timestamp of current window's left edge

	hasSelection       bool
	selAbsT0, selAbsT1 float64

	// Toolbar widgets.
	portSelect   *widget.Select
	connectBtn   *widget.Button
	windowSelect *widget.Select
	scaleSelect  *widget.Select
	pauseBtn     *widget.Button

	// Stat labels.
	statCurrent, statAvg, statPeak, statMin, statSamples, statRate *widget.Label
	selAvg, selPeak, selMin, selSamples, selDuration               *widget.Label
	statusLabel                                                    *widget.Label

	stopUpdate chan struct{}

	initialPort      string
	toolbarContainer *fyne.Container
}

func NewCurrentRangerApp(win fyne.Window, initialPort string) *App {
	a := &App{
		win:        win,
		chart:      NewChartWidget(),
		timeWindow: 30,
		stopUpdate: make(chan struct{}),
	}
	a.chart.OnSelectionChanged = a.onSelectionChanged
	a.chart.OnScrollZoom = a.onScrollZoom
	a.initialPort = initialPort
	return a
}

// Build constructs the full window content and starts the update loop.
func (a *App) Build() fyne.CanvasObject {
	a.buildToolbar()
	statsPanel := a.buildStatsPanel()

	a.statusLabel = widget.NewLabel("Disconnected")

	content := container.NewBorder(
		a.toolbarContainer, a.statusLabel, nil, statsPanel,
		a.chart,
	)

	a.refreshPorts(a.initialPort)
	if a.portSelect.Selected != "" {
		go func() {
			time.Sleep(500 * time.Millisecond)
			fyne.Do(a.connect)
		}()
	}

	go a.updateLoop()

	return content
}

// --- toolbar ----------------------------------------------------------

func (a *App) buildToolbar() {
	a.portSelect = widget.NewSelect(nil, func(string) {})

	refreshBtn := widget.NewButton("Refresh", func() { a.refreshPorts("") })

	a.connectBtn = widget.NewButton("Connect", a.toggleConnect)

	a.windowSelect = widget.NewSelect(windowOptions, a.onWindowChange)
	a.windowSelect.Selected = "30s"

	a.scaleSelect = widget.NewSelect(scaleOptions, a.onScaleChange)
	a.scaleSelect.Selected = "Auto"

	a.pauseBtn = widget.NewButton("Pause", a.togglePause)

	exportBtn := widget.NewButton("Export CSV", a.exportCSV)
	clearBtn := widget.NewButton("Clear", a.clearData)

	a.toolbarContainer = container.NewHBox(
		widget.NewLabel("Port:"), a.portSelect, refreshBtn, a.connectBtn,
		widget.NewSeparator(),
		widget.NewLabel("Window:"), a.windowSelect, a.pauseBtn,
		widget.NewSeparator(),
		widget.NewLabel("Scale:"), a.scaleSelect,
		exportBtn, clearBtn,
	)
}

func (a *App) buildStatsPanel() fyne.CanvasObject {
	mk := func(label string) *widget.Label {
		l := widget.NewLabel("---")
		return l
	}
	a.statCurrent = mk("Current")
	a.statAvg = mk("Average")
	a.statPeak = mk("Peak")
	a.statMin = mk("Minimum")
	a.statSamples = widget.NewLabel("0")
	a.statRate = mk("Rate")

	a.selAvg = mk("Avg")
	a.selPeak = mk("Peak")
	a.selMin = mk("Min")
	a.selSamples = mk("Samples")
	a.selDuration = mk("Duration")

	row := func(name string, val *widget.Label) fyne.CanvasObject {
		return container.NewVBox(widget.NewLabel(name), val)
	}

	live := container.NewVBox(
		widget.NewLabelWithStyle("LIVE STATS", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		row("Current", a.statCurrent),
		row("Average", a.statAvg),
		row("Peak", a.statPeak),
		row("Minimum", a.statMin),
		row("Samples", a.statSamples),
		row("Rate", a.statRate),
		widget.NewSeparator(),
		widget.NewLabelWithStyle("SELECTION", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}),
		row("Avg", a.selAvg),
		row("Peak", a.selPeak),
		row("Min", a.selMin),
		row("Samples", a.selSamples),
		row("Duration", a.selDuration),
	)
	return container.NewVScroll(live)
}

func (a *App) refreshPorts(preferred string) {
	ports := listPorts()
	a.portSelect.Options = ports
	chosen := ""
	if preferred != "" {
		for _, p := range ports {
			if p == preferred {
				chosen = p
			}
		}
	}
	if chosen == "" {
		for _, p := range ports {
			lp := strings.ToLower(p)
			if strings.Contains(lp, "usbmodem") || strings.Contains(lp, "currentranger") {
				chosen = p
				break
			}
		}
	}
	if chosen == "" && len(ports) > 0 {
		chosen = ports[0]
	}
	a.portSelect.SetSelected(chosen)
	a.portSelect.Refresh()
}

// --- connection ---------------------------------------------------------

func (a *App) toggleConnect() {
	if a.reader != nil && a.reader.IsRunning() {
		a.disconnect()
	} else {
		a.connect()
	}
}

func (a *App) connect() {
	port := a.portSelect.Selected
	if port == "" {
		dialog.ShowError(fmt.Errorf("no port selected"), a.win)
		return
	}
	a.reader = NewSerialReader(port)
	a.reader.Start()
	go func() {
		time.Sleep(300 * time.Millisecond)
		fyne.Do(a.checkConnection)
	}()
}

func (a *App) checkConnection() {
	if a.reader == nil {
		return
	}
	if err := a.reader.Error(); err != nil {
		dialog.ShowError(err, a.win)
		a.reader.Stop()
		a.reader = nil
		a.statusLabel.SetText("Connection failed")
		return
	}
	if a.reader.IsRunning() {
		a.connectBtn.SetText("Disconnect")
		a.statusLabel.SetText(fmt.Sprintf("Connected to %s", a.reader.Port()))
	}
}

func (a *App) disconnect() {
	if a.reader != nil {
		a.reader.Stop()
		a.reader = nil
	}
	a.connectBtn.SetText("Connect")
	a.statusLabel.SetText("Disconnected")
}

// --- controls -------------------------------------------------------------

func (a *App) onWindowChange(val string) {
	a.timeWindow = windowSeconds[val]
	if a.paused {
		a.togglePause()
	}
	a.clearSelectionStats()
}

func (a *App) onScaleChange(val string) {
	a.yScale = scaleAmps[val]
	a.tick()
}

func (a *App) togglePause() {
	a.paused = !a.paused
	if a.paused {
		a.pauseBtn.SetText("Resume")
	} else {
		a.pauseBtn.SetText("Pause")
	}
	if a.paused && a.reader != nil {
		ts, cur := a.reader.Snapshot()
		ts, cur = sliceToWindow(ts, cur, a.timeWindow)
		a.pausedTs, a.pausedCur = ts, cur
	} else if !a.paused {
		a.pausedTs, a.pausedCur = nil, nil
	}
}

func (a *App) clearData() {
	if a.reader != nil {
		a.reader.Clear()
	}
	a.statCurrent.SetText("---")
	a.statAvg.SetText("---")
	a.statPeak.SetText("---")
	a.statMin.SetText("---")
	a.statSamples.SetText("0")
}

func (a *App) exportCSV() {
	if a.reader == nil {
		dialog.ShowInformation("Export", "No data to export", a.win)
		return
	}
	ts, cur := a.reader.Snapshot()
	if len(ts) == 0 {
		dialog.ShowInformation("Export", "No data to export", a.win)
		return
	}

	saveDialog := dialog.NewFileSave(func(uc fyne.URIWriteCloser, err error) {
		if err != nil || uc == nil {
			return
		}
		defer uc.Close()
		path := uc.URI().Path()
		n, werr := exportCSV(path, ts, cur, a.hasSelection, a.selAbsT0, a.selAbsT1)
		if werr != nil {
			dialog.ShowError(werr, a.win)
			return
		}
		a.statusLabel.SetText(fmt.Sprintf("Exported %d samples to %s", n, path))
	}, a.win)
	saveDialog.SetFileName(fmt.Sprintf("current_log_%s.csv", time.Now().Format("20060102_150405")))
	saveDialog.SetLocation(defaultExportLocation())
	saveDialog.Show()
}

func defaultExportLocation() fyne.ListableURI {
	uri, err := storage.ListerForURI(storage.NewFileURI("."))
	if err != nil {
		return nil
	}
	return uri
}

// --- selection / zoom callbacks from the chart widget -----------------

func (a *App) onSelectionChanged(x0, x1 float64, active bool) {
	a.hasSelection = active
	if active {
		a.selAbsT0 = a.plotT0 + x0
		a.selAbsT1 = a.plotT0 + x1
	} else {
		a.clearSelectionStats()
	}
}

func (a *App) onScrollZoom(centerPlotX float64, zoomIn bool) {
	// Pause on first zoom interaction, mirroring the Python behavior.
	if !a.paused {
		a.togglePause()
	}
	// The actual axis-range change happens on the next render tick by
	// adjusting timeWindow around the zoom center; here we approximate
	// matplotlib's scroll-to-zoom by shrinking/growing the window.
	scale := 1.3
	if zoomIn {
		scale = 1 / 1.3
	}
	if a.timeWindow <= 0 {
		a.timeWindow = 30
	}
	a.timeWindow = math.Max(1, a.timeWindow*scale)
}

func (a *App) clearSelectionStats() {
	a.hasSelection = false
	a.selAvg.SetText("---")
	a.selPeak.SetText("---")
	a.selMin.SetText("---")
	a.selSamples.SetText("---")
	a.selDuration.SetText("---")
	a.chart.SetSelection(0, 0, false)
}

// --- update loop ----------------------------------------------------------

func (a *App) updateLoop() {
	ticker := time.NewTicker(updateInterval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopUpdate:
			return
		case <-ticker.C:
			fyne.Do(a.tick)
		}
	}
}

func (a *App) tick() {
	if a.reader == nil {
		return
	}
	// IsRunning() and Error() are set together (see reader.go's run()), so
	// once the background read loop dies partway through a session — not
	// just on the initial connect — this is the only place that notices;
	// checking it after Snapshot() would miss it forever, since Snapshot
	// keeps returning the last samples collected before the failure.
	if !a.reader.IsRunning() {
		if err := a.reader.Error(); err != nil {
			a.statusLabel.SetText(fmt.Sprintf("Error: %v", err))
		}
		a.disconnect()
		return
	}
	ts, cur := a.reader.Snapshot()
	if len(cur) == 0 {
		return
	}

	if a.paused {
		a.renderPaused()
		return
	}

	visTs, visCur := sliceToWindow(ts, cur, a.timeWindow)
	if len(visCur) == 0 {
		return
	}

	nowVal := visCur[len(visCur)-1]
	avgVal, peakVal, minVal := stats(visCur)

	a.statCurrent.SetText(formatCurrent(nowVal))
	a.statAvg.SetText(formatCurrent(avgVal))
	a.statPeak.SetText(formatCurrent(peakVal))
	a.statMin.SetText(formatCurrent(minVal))
	a.statSamples.SetText(fmt.Sprintf("%d", len(ts)))

	if len(visTs) > 1 {
		dt := visTs[len(visTs)-1] - visTs[0]
		if dt > 0 {
			a.statRate.SetText(fmt.Sprintf("%.0f Hz", float64(len(visTs))/dt))
		}
	}

	t0 := visTs[0]
	a.plotT0 = t0
	relT := relativeTimes(visTs, t0)

	traceT, traceC := downsample(relT, visCur, maxDisplayPoints)
	avgT, avgC := runningAverage(relT, visCur, maxDisplayPoints)

	yLo, yHi := a.yAxisRange(minVal, peakVal)
	a.chart.SetData(traceT, traceC, avgT, avgC, relT[0], relT[len(relT)-1], yLo, yHi)

	if a.hasSelection {
		a.chart.SetSelection(a.selAbsT0-t0, a.selAbsT1-t0, true)
		a.computeSelectionStats(visTs, visCur)
	}
}

func (a *App) renderPaused() {
	if len(a.pausedTs) == 0 {
		return
	}
	relT := relativeTimes(a.pausedTs, a.plotT0)
	traceT, traceC := downsample(relT, a.pausedCur, maxDisplayPoints)
	avgT, avgC := runningAverage(relT, a.pausedCur, maxDisplayPoints)

	_, peakVal, minVal := stats(a.pausedCur)
	yLo, yHi := a.yAxisRange(minVal, peakVal)
	a.chart.SetData(traceT, traceC, avgT, avgC, relT[0], relT[len(relT)-1], yLo, yHi)

	if a.hasSelection {
		a.computeSelectionStats(a.pausedTs, a.pausedCur)
	}
}

func (a *App) computeSelectionStats(ts, cur []float64) {
	t0, t1 := a.selAbsT0, a.selAbsT1
	var selC []float64
	var selT []float64
	for i, t := range ts {
		if t >= t0 && t <= t1 {
			selC = append(selC, cur[i])
			selT = append(selT, t)
		}
	}
	if len(selC) == 0 {
		a.selAvg.SetText("---")
		a.selPeak.SetText("---")
		a.selMin.SetText("---")
		a.selSamples.SetText("---")
		a.selDuration.SetText("---")
		return
	}
	avg, peak, min := stats(selC)
	a.selAvg.SetText(formatCurrent(avg))
	a.selPeak.SetText(formatCurrent(peak))
	a.selMin.SetText(formatCurrent(min))
	a.selSamples.SetText(fmt.Sprintf("%d", len(selC)))
	duration := 0.0
	if len(selT) > 1 {
		duration = selT[len(selT)-1] - selT[0]
	}
	a.selDuration.SetText(fmt.Sprintf("%.3f s", duration))
	a.statusLabel.SetText(fmt.Sprintf("Selection: %s avg over %.3fs (%d samples)",
		formatCurrent(avg), duration, len(selC)))
}

func (a *App) OnClose() {
	close(a.stopUpdate)
	a.disconnect()
}

// --- numeric helpers --------------------------------------------------

// yAxisRange picks the chart's Y bounds: auto-fit to the visible data with
// a 5% margin, or a fixed -yScale..+yScale range when the user has pinned a
// scale. The pinned range deliberately ignores minVal/peakVal — the whole
// point of a fixed scale is a stable frame that doesn't move with the data;
// samples outside it rail at the plot edge instead (see toPx/toPy in
// chart.go) rather than stretching the axis back out.
func (a *App) yAxisRange(minVal, peakVal float64) (lo, hi float64) {
	if a.yScale > 0 {
		return -a.yScale, a.yScale
	}
	margin := (peakVal - minVal) * 0.05
	if margin == 0 {
		margin = math.Abs(peakVal)*0.1 + 1e-9
	}
	return minVal - margin, peakVal + margin
}

func stats(c []float64) (avg, peak, min float64) {
	if len(c) == 0 {
		return 0, 0, 0
	}
	sum := 0.0
	peak = c[0]
	min = c[0]
	for _, v := range c {
		sum += v
		if v > peak {
			peak = v
		}
		if v < min {
			min = v
		}
	}
	return sum / float64(len(c)), peak, min
}

func sliceToWindow(ts, cur []float64, window float64) ([]float64, []float64) {
	if window <= 0 || len(ts) == 0 {
		return ts, cur
	}
	cutoff := ts[len(ts)-1] - window
	start := 0
	for i, t := range ts {
		if t >= cutoff {
			start = i
			break
		}
	}
	return ts[start:], cur[start:]
}

func relativeTimes(ts []float64, t0 float64) []float64 {
	out := make([]float64, len(ts))
	for i, t := range ts {
		out[i] = t - t0
	}
	return out
}

// downsample reduces (t, c) to at most maxPoints samples using a min/max
// bucket strategy (two points per bucket), mirroring the Python
// _render_lines envelope downsampling so spikes are never averaged away.
func downsample(t, c []float64, maxPoints int) ([]float64, []float64) {
	n := len(t)
	if n <= maxPoints {
		return t, c
	}
	bucketSize := n / (maxPoints / 2)
	if bucketSize < 1 {
		bucketSize = 1
	}
	nBuckets := n / bucketSize
	outT := make([]float64, 0, nBuckets*2)
	outC := make([]float64, 0, nBuckets*2)
	for b := 0; b < nBuckets; b++ {
		lo := b * bucketSize
		hi := lo + bucketSize
		minIdx, maxIdx := lo, lo
		for i := lo; i < hi; i++ {
			if c[i] < c[minIdx] {
				minIdx = i
			}
			if c[i] > c[maxIdx] {
				maxIdx = i
			}
		}
		// Preserve chronological order within the bucket.
		if t[minIdx] <= t[maxIdx] {
			outT = append(outT, t[minIdx], t[maxIdx])
			outC = append(outC, c[minIdx], c[maxIdx])
		} else {
			outT = append(outT, t[maxIdx], t[minIdx])
			outC = append(outC, c[maxIdx], c[minIdx])
		}
	}
	// Tail remainder that didn't fill a whole bucket.
	if trimmed := nBuckets * bucketSize; trimmed < n {
		outT = append(outT, t[trimmed:]...)
		outC = append(outC, c[trimmed:]...)
	}
	return outT, outC
}

// runningAverage computes a simple moving average (matching the Python
// window = min(50, n/4) heuristic) and strides the result down to at most
// maxPoints samples for rendering.
func runningAverage(t, c []float64, maxPoints int) ([]float64, []float64) {
	n := len(c)
	if n <= 10 {
		return nil, nil
	}
	win := n / 4
	if win > 50 {
		win = 50
	}
	if win < 1 {
		win = 1
	}
	avg := movingAverage(c, win)

	if n <= maxPoints {
		return t, avg
	}
	stride := n / maxPoints
	if stride < 1 {
		stride = 1
	}
	var outT, outC []float64
	for i := 0; i < n; i += stride {
		outT = append(outT, t[i])
		outC = append(outC, avg[i])
	}
	return outT, outC
}

// movingAverage is a centered simple moving average ("same" mode, like
// numpy.convolve(..., mode="same")).
func movingAverage(c []float64, win int) []float64 {
	n := len(c)
	out := make([]float64, n)
	half := win / 2
	// Prefix sums for O(n) computation.
	prefix := make([]float64, n+1)
	for i, v := range c {
		prefix[i+1] = prefix[i] + v
	}
	for i := 0; i < n; i++ {
		lo := i - half
		hi := lo + win
		if lo < 0 {
			lo = 0
		}
		if hi > n {
			hi = n
		}
		count := hi - lo
		if count <= 0 {
			out[i] = c[i]
			continue
		}
		out[i] = (prefix[hi] - prefix[lo]) / float64(count)
	}
	return out
}
