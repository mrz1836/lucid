package exports

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/observations"
)

// measEvent builds a KindMeasurement reading with the payload shape
// `lucid obs measurement <metric> <value> [<unit>]` writes. An empty unit is
// left out of the payload, as the parser leaves it out.
func measEvent(id, date, metric string, value any, unit string) observations.Event {
	payload := map[string]any{"metric": metric, "value": value}
	if unit != "" {
		payload["unit"] = unit
	}
	return observations.Event{ID: id, Kind: observations.KindMeasurement, LogicalDate: date, Payload: payload}
}

// measVoid builds the void `lucid obs void <target>` writes, filed under date.
func measVoid(id, date, target string) observations.Event {
	return observations.Event{
		ID: id, Kind: observations.KindMeasurement, LogicalDate: date,
		Payload: map[string]any{}, Tags: []string{},
		Refs: map[string]any{observations.RefCorrects: target, observations.RefVoid: true},
	}
}

// rowIDs lists the ids of rows in order.
func rowIDs(rows []MeasurementRow) []string {
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

// TestBuildMeasurementRows_SameDayKeepsEveryReading proves two blood-pressure
// readings on one day yield four rows — none aggregated — ordered by metric
// (byte order: diastolic before systolic) and then by id.
func TestBuildMeasurementRows_SameDayKeepsEveryReading(t *testing.T) {
	events := []observations.Event{
		measEvent("obs_2026_07_02_003", "2026-07-02", "bp-systolic", "125", "mmHg"),
		measEvent("obs_2026_07_02_004", "2026-07-02", "bp-diastolic", "83", "mmHg"),
		measEvent("obs_2026_07_02_001", "2026-07-02", "bp-systolic", "121", "mmHg"),
		measEvent("obs_2026_07_02_002", "2026-07-02", "bp-diastolic", "79", "mmHg"),
	}
	rows := BuildMeasurementRows(events, MeasurementFilter{})
	assert.Equal(t, []MeasurementRow{
		{Date: "2026-07-02", Metric: "bp-diastolic", Value: "79", Unit: "mmHg", ID: "obs_2026_07_02_002"},
		{Date: "2026-07-02", Metric: "bp-diastolic", Value: "83", Unit: "mmHg", ID: "obs_2026_07_02_004"},
		{Date: "2026-07-02", Metric: "bp-systolic", Value: "121", Unit: "mmHg", ID: "obs_2026_07_02_001"},
		{Date: "2026-07-02", Metric: "bp-systolic", Value: "125", Unit: "mmHg", ID: "obs_2026_07_02_003"},
	}, rows)
}

// TestBuildMeasurementRows_NumericRuleSkipsAndKeepsVerbatim proves only a
// stored text token matching `^-?[0-9]+(\.[0-9]+)?$` is exported, byte-verbatim
// (5.50 stays 5.50), and every other value — a non-numeric or malformed
// token, an empty value, a JSON number, a partial capture, a reading with no
// metric — is skipped, never coerced into a row.
func TestBuildMeasurementRows_NumericRuleSkipsAndKeepsVerbatim(t *testing.T) {
	const day = "2026-07-02"
	kept := []observations.Event{
		measEvent("obs_2026_07_02_001", day, "sleep-hours", "5.50", "h"),
		measEvent("obs_2026_07_02_002", day, "temp-delta", "-3", "C"),
		measEvent("obs_2026_07_02_003", day, "steps", "0", ""),
		measEvent("obs_2026_07_02_004", day, "ldl-c", "104", "mg/dL"),
	}
	skipped := []observations.Event{
		measEvent("obs_2026_07_02_010", day, "flow", "heavy", ""),
		measEvent("obs_2026_07_02_011", day, "steps", "1,128", ""),
		measEvent("obs_2026_07_02_012", day, "steps", ".5", ""),
		measEvent("obs_2026_07_02_013", day, "steps", "<10", ""),
		measEvent("obs_2026_07_02_014", day, "steps", ">60", ""),
		measEvent("obs_2026_07_02_015", day, "steps", "", ""),
		measEvent("obs_2026_07_02_016", day, "steps", "1.", ""),
		measEvent("obs_2026_07_02_017", day, "steps", "+5", ""),
		measEvent("obs_2026_07_02_018", day, "steps", "1e3", ""),
		measEvent("obs_2026_07_02_019", day, "steps", "-", ""),
		measEvent("obs_2026_07_02_020", day, "steps", "1.2.3", ""),
		measEvent("obs_2026_07_02_021", day, "steps", float64(96), ""), // a hand-edited JSON number
		{ // a partial capture: `obs measurement steps` with no value
			ID: "obs_2026_07_02_022", Kind: observations.KindMeasurement, LogicalDate: day,
			Payload: map[string]any{"note": "steps", "parse": observations.ParseMarkerPartial},
		},
		{ // a partial marker on an otherwise numeric payload is still skipped
			ID: "obs_2026_07_02_023", Kind: observations.KindMeasurement, LogicalDate: day,
			Payload: map[string]any{"metric": "steps", "value": "12", "parse": observations.ParseMarkerPartial},
		},
		{ // no metric
			ID: "obs_2026_07_02_024", Kind: observations.KindMeasurement, LogicalDate: day,
			Payload: map[string]any{"value": "12"},
		},
		measEvent("obs_2026_07_02_025", day, "", "12", ""), // an empty metric
		{ // a different kind with a numeric value
			ID: "obs_2026_07_02_026", Kind: observations.KindPain, LogicalDate: day,
			Payload: map[string]any{"metric": "steps", "value": "12"},
		},
	}
	rows := BuildMeasurementRows(append(append([]observations.Event{}, kept...), skipped...), MeasurementFilter{})
	assert.Equal(t, []string{"obs_2026_07_02_004", "obs_2026_07_02_001", "obs_2026_07_02_003", "obs_2026_07_02_002"}, rowIDs(rows),
		"only the four plain decimals survive, in date/metric/id order")
	values := map[string]string{}
	for _, r := range rows {
		values[r.Metric] = r.Value
	}
	assert.Equal(t, map[string]string{"sleep-hours": "5.50", "temp-delta": "-3", "steps": "0", "ldl-c": "104"}, values,
		"each kept value is byte-verbatim")
}

// TestBuildMeasurementRows_UnitPassThrough proves a unit is carried byte-exact
// (case kept) and never converted: one metric in two units keeps each unit, a
// reading with no unit keeps an empty unit, and a non-string unit reads as
// absent.
func TestBuildMeasurementRows_UnitPassThrough(t *testing.T) {
	events := []observations.Event{
		measEvent("obs_2026_07_01_001", "2026-07-01", "bp-systolic", "121", "mmHg"),
		measEvent("obs_2026_07_02_001", "2026-07-02", "weight", "70.5", "kg"),
		measEvent("obs_2026_07_03_001", "2026-07-03", "weight", "165.2", "lb"),
		measEvent("obs_2026_07_04_001", "2026-07-04", "weight", "70.3", ""),
		measEvent("obs_2026_07_05_001", "2026-07-05", "weight", "70.5", ""),
	}
	events[4].Payload["unit"] = float64(3) // a hand-edited non-string unit
	rows := BuildMeasurementRows(events, MeasurementFilter{})
	require.Len(t, rows, 5)
	assert.Equal(t, []string{"mmHg", "kg", "lb", "", ""}, []string{rows[0].Unit, rows[1].Unit, rows[2].Unit, rows[3].Unit, rows[4].Unit})
	assert.Equal(t, []string{"121", "70.5", "165.2", "70.3", "70.5"},
		[]string{rows[0].Value, rows[1].Value, rows[2].Value, rows[3].Value, rows[4].Value},
		"no value is converted between units")
}

// TestBuildMeasurementRows_DropsVoidedReading proves a voided reading drops
// out and its same-day sibling survives, a void filed after the window's end
// still drops its in-window target (the fold runs over all history before the
// window), and a void naming a missing id drops only itself.
func TestBuildMeasurementRows_DropsVoidedReading(t *testing.T) {
	t.Run("same_day_survivor", func(t *testing.T) {
		events := []observations.Event{
			measEvent("obs_2026_07_02_001", "2026-07-02", "weight", "70.5", "kg"),
			measEvent("obs_2026_07_02_002", "2026-07-02", "weight", "75.0", "kg"),
			measVoid("obs_2026_07_02_003", "2026-07-02", "obs_2026_07_02_002"),
		}
		rows := BuildMeasurementRows(events, MeasurementFilter{})
		assert.Equal(t, []string{"obs_2026_07_02_001"}, rowIDs(rows))
	})

	t.Run("void_after_window_end", func(t *testing.T) {
		events := []observations.Event{
			measEvent("obs_2026_07_02_001", "2026-07-02", "weight", "70.5", "kg"),
			measEvent("obs_2026_07_03_001", "2026-07-03", "bp-systolic", "121", "mmHg"),
			measVoid("obs_2026_07_20_001", "2026-07-20", "obs_2026_07_03_001"),
		}
		rows := BuildMeasurementRows(events, MeasurementFilter{Start: "2026-07-01", End: "2026-07-05"})
		assert.Equal(t, []string{"obs_2026_07_02_001"}, rowIDs(rows))
	})

	t.Run("void_of_missing_id", func(t *testing.T) {
		events := []observations.Event{
			measEvent("obs_2026_07_02_001", "2026-07-02", "weight", "70.5", "kg"),
			measVoid("obs_2026_07_02_002", "2026-07-02", "obs_2026_06_30_009"),
		}
		rows := BuildMeasurementRows(events, MeasurementFilter{})
		assert.Equal(t, []string{"obs_2026_07_02_001"}, rowIDs(rows), "the void is never a row; nothing else drops")
	})
}

// TestBuildMeasurementRows_WindowAndMetricFilter proves the window is
// inclusive at both ends, a reading after the end is excluded, and the metric
// filter is one exact, case-sensitive match (an unknown slug keeps nothing).
func TestBuildMeasurementRows_WindowAndMetricFilter(t *testing.T) {
	events := []observations.Event{
		measEvent("obs_2026_06_30_001", "2026-06-30", "weight", "70.9", "kg"),
		measEvent("obs_2026_07_01_001", "2026-07-01", "weight", "70.5", "kg"),
		measEvent("obs_2026_07_01_002", "2026-07-01", "bp-systolic", "121", "mmHg"),
		measEvent("obs_2026_07_05_001", "2026-07-05", "weight", "70.3", "kg"),
		measEvent("obs_2026_07_06_001", "2026-07-06", "weight", "70.1", "kg"), // after End
	}

	window := MeasurementFilter{Start: "2026-07-01", End: "2026-07-05"}
	assert.Equal(t, []string{"obs_2026_07_01_002", "obs_2026_07_01_001", "obs_2026_07_05_001"},
		rowIDs(BuildMeasurementRows(events, window)), "Start and End are inclusive; outside readings drop")

	assert.Len(t, BuildMeasurementRows(events, MeasurementFilter{End: "2026-07-05"}), 4,
		"no Start is unbounded below; a reading after End is still excluded")
	assert.Len(t, BuildMeasurementRows(events, MeasurementFilter{}), 5, "an empty filter keeps every reading")

	weight := BuildMeasurementRows(events, MeasurementFilter{Start: "2026-07-01", End: "2026-07-05", Metric: "weight"})
	assert.Equal(t, []string{"obs_2026_07_01_001", "obs_2026_07_05_001"}, rowIDs(weight), "only the named metric")

	assert.Empty(t, BuildMeasurementRows(events, MeasurementFilter{Metric: "Weight"}), "the match is case-sensitive")
	assert.Empty(t, BuildMeasurementRows(events, MeasurementFilter{Metric: "no-such-metric"}), "an unknown slug keeps nothing")
	assert.Empty(t, BuildMeasurementRows(events, MeasurementFilter{Start: "2026-08-01", End: "2026-07-05"}),
		"a start after the end keeps nothing")
}

// TestBuildMeasurementRows_NeverReadsNoteOrTags proves a reading's note and
// tags never reach a row or the CSV — the no-note guarantee is load-bearing.
func TestBuildMeasurementRows_NeverReadsNoteOrTags(t *testing.T) {
	const sentinel = "SENTINEL-NOTE-XYZ"
	e := measEvent("obs_2026_07_02_001", "2026-07-02", "ldl-c", "104", "mg/dL")
	e.Payload["note"] = sentinel + " fasting draw"
	e.Tags = []string{sentinel}
	rows := BuildMeasurementRows([]observations.Event{e}, MeasurementFilter{})
	require.Len(t, rows, 1)
	r := rows[0]
	for _, field := range []string{r.Date, r.Metric, r.Value, r.Unit, r.ID} {
		assert.NotContains(t, field, sentinel)
	}
	assert.NotContains(t, MeasurementsCSV(rows), sentinel)
}

// TestMeasurementsCSV_HeaderOnlyTrailingNewline proves no rows renders exactly
// the header, newline-terminated.
func TestMeasurementsCSV_HeaderOnlyTrailingNewline(t *testing.T) {
	assert.Equal(t, "logical_date,metric,value,unit,id\n", MeasurementsCSV(nil))
	assert.Equal(t, "logical_date,metric,value,unit,id\n", MeasurementsCSV(BuildMeasurementRows(nil, MeasurementFilter{})))
}

// TestMeasurementsCSV_DeterministicAcrossInputOrder proves the same readings
// in any input order render byte-identical CSV, and reruns are byte-equal.
func TestMeasurementsCSV_DeterministicAcrossInputOrder(t *testing.T) {
	a := measEvent("obs_2026_07_01_001", "2026-07-01", "weight", "70.5", "kg")
	b := measEvent("obs_2026_07_01_002", "2026-07-01", "bp-systolic", "121", "mmHg")
	c := measEvent("obs_2026_07_03_001", "2026-07-03", "a1c", "5.6", "%")
	d := measEvent("obs_2026_07_03_002", "2026-07-03", "ldl-c", "104", "mg/dL")
	v := measVoid("obs_2026_07_04_001", "2026-07-04", "obs_2026_07_03_002")

	const want = "logical_date,metric,value,unit,id\n" +
		"2026-07-01,bp-systolic,121,mmHg,obs_2026_07_01_002\n" +
		"2026-07-01,weight,70.5,kg,obs_2026_07_01_001\n" +
		"2026-07-03,a1c,5.6,%,obs_2026_07_03_001\n"

	for _, perm := range [][]observations.Event{
		{a, b, c, d, v},
		{v, d, c, b, a},
		{c, v, a, d, b},
	} {
		assert.Equal(t, want, MeasurementsCSV(BuildMeasurementRows(perm, MeasurementFilter{})))
	}
	first := MeasurementsCSV(BuildMeasurementRows([]observations.Event{a, b, c, d, v}, MeasurementFilter{}))
	assert.Equal(t, first, MeasurementsCSV(BuildMeasurementRows([]observations.Event{a, b, c, d, v}, MeasurementFilter{})))
	assert.True(t, strings.HasSuffix(first, "\n"), "the file ends with a trailing newline")
}

// TestMeasurementsCSV_QuotesCommaOrQuoteCells proves a cell holding a comma,
// a double quote, or a line break is RFC 4180-quoted (inner quotes doubled),
// and every ordinary cell stays bare.
func TestMeasurementsCSV_QuotesCommaOrQuoteCells(t *testing.T) {
	rows := []MeasurementRow{
		{Date: "2026-07-01", Metric: "a,b", Value: "1", Unit: `in"`, ID: "obs_2026_07_01_001"},
		{Date: "2026-07-01", Metric: "c\nd", Value: "2", Unit: "x\ry", ID: "obs_2026_07_01_002"},
		{Date: "2026-07-01", Metric: "weight", Value: "70.5", Unit: "kg", ID: "obs_2026_07_01_003"},
	}
	assert.Equal(t, "logical_date,metric,value,unit,id\n"+
		`2026-07-01,"a,b",1,"in""",obs_2026_07_01_001`+"\n"+
		"2026-07-01,\"c\nd\",2,\"x\ry\",obs_2026_07_01_002\n"+
		"2026-07-01,weight,70.5,kg,obs_2026_07_01_003\n",
		MeasurementsCSV(rows))
}
