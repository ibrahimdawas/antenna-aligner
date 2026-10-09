package engine

import (
	"encoding/csv"
	"fmt"
	"io"
	"strconv"
)

// ExportCSV writes all positions as CSV with UTF-8 BOM for Excel compatibility.
func ExportCSV(positions []Position, w io.Writer) error {
	// Write UTF-8 BOM
	if _, err := w.Write([]byte{0xEF, 0xBB, 0xBF}); err != nil {
		return err
	}

	cw := csv.NewWriter(w)
	defer cw.Flush()

	// Header
	if err := cw.Write([]string{
		"ID", "Phase", "Azimuth Step", "Azimuth Deg", "Tilt Step", "Tilt Deg",
		"Avg Score", "Avg RSRP", "Avg SINR", "Avg RSRQ",
		"Cell ID", "Tower Switch", "Sample Count", "Time",
	}); err != nil {
		return err
	}

	for _, p := range positions {
		azDeg := p.AzimuthStep * stepDegForPhase(p.Phase)
		tiltDeg := p.TiltStep * 5

		row := []string{
			strconv.Itoa(p.ID),
			string(p.Phase),
			strconv.Itoa(p.AzimuthStep),
			strconv.Itoa(azDeg),
			strconv.Itoa(p.TiltStep),
			strconv.Itoa(tiltDeg),
			fmt.Sprintf("%.1f", p.AvgScore),
			ptrFmt(p.AvgRSRP),
			ptrFmt(p.AvgSINR),
			ptrFmt(p.AvgRSRQ),
			p.CellID,
			strconv.FormatBool(p.TowerSwitch),
			strconv.Itoa(p.SampleCount),
			p.Time.Format("2006-01-02 15:04:05"),
		}
		if err := cw.Write(row); err != nil {
			return err
		}
	}

	return cw.Error()
}

func ptrFmt(v *float64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%.1f", *v)
}

func stepDegForPhase(phase Phase) int {
	switch phase {
	case PhaseCoarse:
		return 30
	case PhaseFineAz10:
		return 10
	case PhaseFineAz5, PhaseRecheck:
		return 5
	case PhaseTilt:
		return 5
	default:
		return 1
	}
}
