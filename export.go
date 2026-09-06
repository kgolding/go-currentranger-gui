package main

import (
	"fmt"
	"os"
)

// exportCSV writes time_s,current_a rows to filename. If selActive is true,
// only samples within [t0Sel, t1Sel] (absolute Unix seconds) are written,
// mirroring the Python "export selection if one is active" behavior.
// Returns the number of samples written.
func exportCSV(filename string, ts, cur []float64, selActive bool, t0Sel, t1Sel float64) (int, error) {
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

	if _, err := f.WriteString("time_s,current_a\n"); err != nil {
		return 0, err
	}
	t0 := outTs[0]
	for i, t := range outTs {
		if _, err := fmt.Fprintf(f, "%.6f,%.12e\n", t-t0, outCur[i]); err != nil {
			return 0, err
		}
	}
	return len(outTs), nil
}
