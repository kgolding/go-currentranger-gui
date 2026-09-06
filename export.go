package main

import (
	"fmt"
	"os"
)

// exportCSV writes time_s,current_a rows to filename (plus a power_w column
// when voltage > 0). If selActive is true, only samples within
// [t0Sel, t1Sel] (absolute Unix seconds) are written, mirroring the Python
// "export selection if one is active" behavior. Returns the number of
// samples written.
func exportCSV(filename string, ts, cur []float64, selActive bool, t0Sel, t1Sel, voltage float64) (int, error) {
	if len(ts) == 0 {
		return 0, fmt.Errorf("no data to export")
	}

	outTs := ts
	outCur := cur
	if selActive {
		var fts, fcur []float64
		for i, t := range ts {
			if t >= t0Sel && t <= t1Sel {
				fts = append(fts, t)
				fcur = append(fcur, cur[i])
			}
		}
		if len(fts) == 0 {
			return 0, fmt.Errorf("no samples in selection")
		}
		outTs, outCur = fts, fcur
	}

	f, err := os.Create(filename)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	header := "time_s,current_a\n"
	if voltage > 0 {
		header = "time_s,current_a,power_w\n"
	}
	if _, err := f.WriteString(header); err != nil {
		return 0, err
	}
	t0 := outTs[0]
	for i, t := range outTs {
		if voltage > 0 {
			if _, err := fmt.Fprintf(f, "%.6f,%.12e,%.6e\n", t-t0, outCur[i], outCur[i]*voltage); err != nil {
				return 0, err
			}
			continue
		}
		if _, err := fmt.Fprintf(f, "%.6f,%.12e\n", t-t0, outCur[i]); err != nil {
			return 0, err
		}
	}
	return len(outTs), nil
}
