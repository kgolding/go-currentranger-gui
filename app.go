package main

import (
	"fmt"
	"math"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/storage"
	"fyne.io/fyne/v2/widget"
)

const (
	updateInterval    = 50 * time.Millisecond
	maxDisplayPoints  = 4000
)

var windowOptions = []string{"1s", "2s", "3s", "4s", "5s", "10s", "30s", "60s", "5m", "All"}
var windowSeconds = map[string]float64{
	"1s": 1, "2s": 2, "3s": 3, "4s": 4, "5s": 5, "10s": 10, "30s": 30, "60s": 60, "5m": 300, "All": 0,
}

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

var rollAvgOptions = []string{
	"5ms", "10ms", "50ms", "100ms", "250ms", "500ms", "750ms",
	"1s", "2s", "3s", "4s", "5s", "10s", "20s", "30s",
}
var rollAvgMillis = map[string]float64{
	"5ms": 5, "10ms": 10, "50ms": 50, "100ms": 100, "250ms": 250, "500ms": 500, "750ms": 750,
	"1s": 1000, "2s": 2000, "3s": 3000, "4s": 4000, "5s": 5000,
	"10s": 10000, "20s": 20000, "30s": 30000,
}

// App owns all UI state and the live-update loop. It mirrors
// CurrentRangerApp from the Python version.
type App struct {
	win    fyne.Window
	reader *SerialReader
	chart  *ChartWidget

	timeWindow   float64
	yScale       float64 // 0 = auto-range; otherwise a fixed full-scale amps value
	rollingAvgMs float64 // rolling-average window, in milliseconds
	paused       bool
	pausedTs   []float64
	pausedCur  []float64
	plotT0     float64 // absolute timestamp of current window's left edge

	hasSelection       bool
	selAbsT0, selAbsT1 float64

	// Toolbar widgets.
	portSelect   *widget.Select
	connectBtn   *widget.Button
	windowSelect  *widget.Select
	scaleSelect   *widget.Select
	rollAvgSelect *widget.Select
	pauseBtn     *widget.Button

	// Stat labels.
	statCurrent, statAvg, statPeak, statMin, statSamples, statRate *widget.Label
	selAvg, selPeak, selMin, selSamples, selDuration               *widget.Label

	stopUpdate chan struct{}

	initialPort      string
	toolbarContainer *fyne.Container
	portBox          *fyne.Container // fixed-width wrapper around portSelect
}

func NewCurrentRangerApp(win fyne.Window, initialPort string) *App {
	a := &App{
		win:          win,
		chart:        NewChartWidget(),
		timeWindow:   10,
		rollingAvgMs: 1000,
		stopUpdate:   make(chan struct{}),
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

	content := container.New(&mainLayout{},
		a.toolbarContainer, statsPanel, a.chart,
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
	// Wrapped in a fixed-size box so the 50%-wider target sticks — inside
	// a plain HBox, the container would just re-shrink it to MinSize on
	// every layout pass. refreshPorts() re-sizes this box once real port
	// names are loaded, since MinSize() is near-empty before that.
	portMin := a.portSelect.MinSize()
	a.portBox = container.New(
		layout.NewGridWrapLayout(fyne.NewSize(portMin.Width*1.5, portMin.Height)),
		a.portSelect,
	)

	refreshBtn := widget.NewButton("Refresh", func() { a.refreshPorts("") })
	a.connectBtn = widget.NewButton("Connect", a.toggleConnect)
	a.pauseBtn = widget.NewButton("Pause", a.togglePause)
	exportBtn := widget.NewButton("Export CSV", a.exportCSV)
	clearBtn := widget.NewButton("Clear", a.clearData)

	a.toolbarContainer = container.NewHBox(
		widget.NewLabel("Port:"), a.portBox, refreshBtn, a.connectBtn,
		layout.NewSpacer(),
		exportBtn, clearBtn, a.pauseBtn,
	)
}

// buildStatsPanel lays out the sidebar as three always-horizontal
// (label-beside-value) form groups: SETTINGS (moved here from the
// toolbar), LIVE STATS, and SELECTION.
func (a *App) buildStatsPanel() fyne.CanvasObject {
	a.windowSelect = widget.NewSelect(windowOptions, a.onWindowChange)
	a.windowSelect.Selected = "10s"

	a.scaleSelect = widget.NewSelect(scaleOptions, a.onScaleChange)
	a.scaleSelect.Selected = "Auto"

	a.rollAvgSelect = widget.NewSelect(rollAvgOptions, a.onRollAvgChange)
	a.rollAvgSelect.Selected = "1s"

	settings := container.New(layout.NewFormLayout(),
		widget.NewLabel("Window"), a.windowSelect,
		widget.NewLabel("Scale"), a.scaleSelect,
		widget.NewLabel("Roll Avg"), a.rollAvgSelect,
	)

	mk := func() *widget.Label { return widget.NewLabel("---") }
	a.statCurrent = mk()
	a.statAvg = mk()
	a.statPeak = mk()
	a.statMin = mk()
	a.statSamples = widget.NewLabel("0")
	a.statRate = mk()

	live := container.New(layout.NewFormLayout(),
		widget.NewLabel("Current"), a.statCurrent,
		widget.NewLabel("Average"), a.statAvg,
		widget.NewLabel("Peak"), a.statPeak,
		widget.NewLabel("Minimum"), a.statMin,
		widget.NewLabel("Samples"), a.statSamples,
		widget.NewLabel("Rate"), a.statRate,
	)

	a.selAvg = mk()
	a.selPeak = mk()
	a.selMin = mk()
	a.selSamples = mk()
	a.selDuration = mk()

	sel := container.New(layout.NewFormLayout(),
		widget.NewLabel("Avg"), a.selAvg,
		widget.NewLabel("Peak"), a.selPeak,
		widget.NewLabel("Min"), a.selMin,
		widget.NewLabel("Samples"), a.selSamples,
		widget.NewLabel("Duration"), a.selDuration,
	)

	header := func(title string) fyne.CanvasObject {
		return widget.NewLabelWithStyle(title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}

	content := container.NewVBox(
		header("SETTINGS"), settings,
		widget.NewSeparator(),
		header("LIVE STATS"), live,
		widget.NewSeparator(),
		header("SELECTION"), sel,
	)
	return container.NewVScroll(content)
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

	// Options weren't loaded yet when portBox was first sized in
	// buildToolbar, so MinSize() was near-empty; re-derive the 50%-wider
	// target now that real port names (e.g. "/dev/ttyACM0") are in.
	portMin := a.portSelect.MinSize()
	a.portBox.Layout = layout.NewGridWrapLayout(fyne.NewSize(portMin.Width*1.5, portMin.Height))
	a.portBox.Refresh()
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
		return
	}
	if a.reader.IsRunning() {
		a.connectBtn.SetText("Disconnect")
	}
}

func (a *App) disconnect() {
	if a.reader != nil {
		a.reader.Stop()
		a.reader = nil
	}
	a.connectBtn.SetText("Connect")
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
	fyne.Do(a.tick)
}

func (a *App) onRollAvgChange(val string) {
	a.rollingAvgMs = rollAvgMillis[val]
	fyne.Do(a.tick)
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
		dialog.ShowInformation("Export", fmt.Sprintf("Exported %d samples to %s", n, path), a.win)
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
			dialog.ShowError(err, a.win)
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
	rollT, rollC := timeRollingAverage(relT, visCur, a.rollingAvgMs/1000, maxDisplayPoints)

	yLo, yHi := a.yAxisRange(minVal, peakVal)
	a.chart.SetData(traceT, traceC, avgT, avgC, rollT, rollC, relT[0], relT[len(relT)-1], yLo, yHi)

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
	rollT, rollC := timeRollingAverage(relT, a.pausedCur, a.rollingAvgMs/1000, maxDisplayPoints)

	_, peakVal, minVal := stats(a.pausedCur)
	yLo, yHi := a.yAxisRange(minVal, peakVal)
	a.chart.SetData(traceT, traceC, avgT, avgC, rollT, rollC, relT[0], relT[len(relT)-1], yLo, yHi)

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

// timeRollingAverage computes a trailing moving average over a real-time
// window (windowSec), unlike runningAverage's sample-count window — the
// averaging window stays a fixed duration (e.g. ~1s of wall-clock data)
// regardless of the current sample rate. t must be sorted ascending.
func timeRollingAverage(t, c []float64, windowSec float64, maxPoints int) (outT, outC []float64) {
	n := len(c)
	if n == 0 || windowSec <= 0 {
		return nil, nil
	}
	avg := make([]float64, n)
	sum := 0.0
	start := 0
	for i := 0; i < n; i++ {
		sum += c[i]
		for t[i]-t[start] > windowSec {
			sum -= c[start]
			start++
		}
		avg[i] = sum / float64(i-start+1)
	}

	if n <= maxPoints {
		return t, avg
	}
	stride := n / maxPoints
	if stride < 1 {
		stride = 1
	}
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
