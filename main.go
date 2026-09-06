// Command currentranger is a real-time current draw monitor for the
// LowPowerLab CurrentRanger, read over USB serial. It is a Go/Fyne port
// of the original Python/Tkinter+matplotlib tool.
//
// Usage:
//
//	go run . --port /dev/cu.usbmodemXXXX
//
// Requires:
//
//	go get fyne.io/fyne/v2
//	go get go.bug.st/serial
//	go get golang.org/x/image
package main

import (
	"flag"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
)

func main() {
	port := flag.String("port", "", "Serial port (e.g. /dev/cu.usbmodem1234)")
	flag.StringVar(port, "p", "", "Serial port (shorthand for --port)")
	flag.Parse()

	a := app.NewWithID("io.sasq.currentranger")
	a.Settings().SetTheme(darkTheme{})

	w := a.NewWindow("CurrentRanger Monitor")
	w.Resize(fyne.NewSize(1200, 850))

	crApp := NewCurrentRangerApp(w, *port)
	w.SetContent(crApp.Build())
	w.SetOnClosed(crApp.OnClose)

	w.ShowAndRun()
}
