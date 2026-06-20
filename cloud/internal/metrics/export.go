package metrics

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"
)

// ExportRow is one hourly rollup row for CSV/JSON export (Story 30.4).
type ExportRow struct {
	Hour     string `json:"hour"`
	Metric   string `json:"metric"`
	Country  string `json:"country"`
	SumValue int64  `json:"sum_value"`
	MaxValue int64  `json:"max_value"`
	Samples  int    `json:"samples"`
}

// exportHeader is the CSV header, kept in one place so the test and the
// renderer agree.
var exportHeader = []string{"hour", "metric", "country", "sum_value", "max_value", "samples"}

// RenderCSV writes rows as CSV with a header line.
func RenderCSV(w io.Writer, rows []ExportRow) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(exportHeader); err != nil {
		return err
	}
	for _, r := range rows {
		rec := []string{
			r.Hour,
			r.Metric,
			r.Country,
			strconv.FormatInt(r.SumValue, 10),
			strconv.FormatInt(r.MaxValue, 10),
			strconv.Itoa(r.Samples),
		}
		if err := cw.Write(rec); err != nil {
			return err
		}
	}
	cw.Flush()
	return cw.Error()
}

// RenderJSON writes rows as a JSON array. A nil slice renders as `[]`.
func RenderJSON(w io.Writer, rows []ExportRow) error {
	if rows == nil {
		rows = []ExportRow{}
	}
	enc := json.NewEncoder(w)
	return enc.Encode(rows)
}
