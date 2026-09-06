# CurrentRanger Monitor (Go / Fyne port)

A port of the Python Tkinter + matplotlib CurrentRanger monitor to Go,
using [Fyne](https://fyne.io) for the UI.

## Build

Requires Go 1.21+ and a normal internet connection (this environment's
sandbox couldn't fetch modules, so this hasn't been compiled — please
build it once locally and report back anything that needs tweaking):

```bash
cd currentranger-go
go mod tidy
go build -o currentranger .
./currentranger --port /dev/cu.usbmodem1234
```

On Linux you'll also need Fyne's usual system deps (gcc, libgl1-mesa-dev,
xorg-dev) since Fyne uses OpenGL via cgo.

## What changed vs. the Python version

- **Plotting**: matplotlib has no Fyne equivalent, so the chart is a
  custom `ChartWidget` (`chart.go`) that rasterizes the trace, running
  average, grid, and axis labels into an `image.RGBA` each frame via
  `canvas.Raster`. Text is drawn with `golang.org/x/image/font/basicfont`
  (a built-in bitmap font) rather than a system font, so labels look
  blocky — swap in a TTF via `golang.org/x/image/font/opentype` if you
  want nicer typography.
- **Downsampling**: reimplemented the min/max-per-bucket envelope
  downsampling from `_render_lines` in `downsample()` (`app.go`), so
  spikes survive downsampling instead of being averaged away.
- **Running average**: a prefix-sum moving average (`movingAverage`)
  replaces `numpy.convolve(..., mode="same")`.
- **Threading**: the serial read loop (`reader.go`) is a goroutine
  instead of a Python `threading.Thread`, guarded by a `sync.Mutex`
  exactly like the original's `self.lock`. UI updates happen on a
  `time.Ticker` (50 ms, matching `UPDATE_INTERVAL_MS`) dispatched via
  `fyne.Do` for thread-safety — this requires **Fyne v2.5+**.
- **Selection drag / scroll-zoom**: implemented via Fyne's
  `Draggable`/`Scrollable`/`Tappable` interfaces on `ChartWidget`
  instead of matplotlib's `mpl_connect` mouse events. Scroll-zoom
  approximates the original by shrinking/growing the visible time
  window rather than matplotlib's arbitrary pixel-anchored zoom.
- **Menu bar / About dialog**: dropped for brevity — easy to add back
  with `fyne.NewMainMenu` if you want the macOS "About" entry.

## Things worth double-checking once you can compile

- `reader.go`'s `isTimeoutErr` assumes `go.bug.st/serial` surfaces read
  timeouts in a specific way; verify against the installed version and
  adjust if `ReadString` behaves differently (e.g. always returns
  `(0, nil)` on timeout, which the current code already tolerates, or
  returns a distinct timeout error type you should check with
  `errors.As` instead of a string match).
- Serial port permissions/naming on Linux (`/dev/ttyACM0` etc.) vs.
  macOS (`/dev/cu.usbmodemXXXX`) — `--port` accepts whatever
  `go.bug.st/serial`'s `GetPortsList()` returns for your OS.
- The `basicfont` labels are tiny; consider a TTF-based renderer if you
  want closer visual parity with the "SF Mono" look of the original.
