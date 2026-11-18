package router

import (
	"context"
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mrz1836/lucid/internal/agents/reflection"
	"github.com/mrz1836/lucid/internal/identity"
	"github.com/mrz1836/lucid/internal/provider"
	"github.com/mrz1836/lucid/internal/storage"
)

// The move-ref fixtures use synthetic people only: two people named "Sam"
// ("Sam" and "Sam Rivera"), plus "Jordan Lee".

// mention is one scripted extraction mention.
type mention struct {
	name string
	aka  []string
}

// m builds a scripted mention.
func m(name string, aka ...string) mention { return mention{name: name, aka: aka} }

// extractionFor renders a well-formed structuring reply naming the mentions.
func extractionFor(t *testing.T, mentions ...mention) string {
	t.Helper()
	type person struct {
		DisplayName string   `json:"display_name"`
		Aka         []string `json:"aka,omitempty"`
	}
	people := make([]person, 0, len(mentions))
	for _, mn := range mentions {
		people = append(people, person{DisplayName: mn.name, Aka: mn.aka})
	}
	b, err := json.Marshal(map[string]any{
		"emotions": []any{},
		"themes":   []any{},
		"people":   people,
		"notes":    "a synthetic entry",
	})
	require.NoError(t, err)
	return string(b)
}

// structure captures one raw entry and structures it through the real
// compose with a scripted extraction naming the mentions, returning the raw id.
func (f *idFixture) structure(mentions ...mention) string {
	f.t.Helper()
	at := f.now()
	id, _ := writeRawAt(f.t, f.a, f.home, "A synthetic entry.", at)
	f.restructure(id, mentions...)
	return id
}

// restructure re-runs Structuring over an existing raw entry (what `lucid
// structure <raw_id> --force` does), with a scripted extraction.
func (f *idFixture) restructure(id string, mentions ...mention) {
	f.t.Helper()
	p := &provider.Fake{Script: []provider.Exchange{{Content: extractionFor(f.t, mentions...)}}}
	res, err := f.r.Structure(context.Background(), StructureRequest{RawID: id, Now: f.now(), Provider: p})
	require.NoError(f.t, err)
	require.True(f.t, res.Wrote, "structure %s must write its artifact", id)
}

// moveRef runs the verb and requires success.
func (f *idFixture) moveRef(raw, from, to string) PersonIdentityResult {
	f.t.Helper()
	res, err := f.r.PersonMoveRef(PersonMoveRefRequest{RawID: raw, From: from, To: to, Now: f.now()})
	require.NoError(f.t, err)
	return res
}

// view renders the /person view of a key.
func (f *idFixture) view(key string) string {
	f.t.Helper()
	res, err := f.r.Person(PersonRequest{Name: key})
	require.NoError(f.t, err)
	require.True(f.t, res.Matched)
	return res.Text
}

// keyOf reads the key a structured raw entry recorded for its one mention.
func (f *idFixture) keyOf(raw string) string {
	f.t.Helper()
	art, err := f.a.ReadProcessed(raw)
	require.NoError(f.t, err)
	require.Len(f.t, art.People, 1)
	return art.People[0].PersonKey
}

// acceptInsightCiting writes one accepted insight citing raw and returns its
// id.
func (f *idFixture) acceptInsightCiting(raw string) string {
	f.t.Helper()
	res, err := f.a.WriteInsight(storage.Insight{
		CreatedAt: f.now(),
		Status:    storage.InsightStatusAccepted,
		Provenance: storage.InsightProvenance{
			RawEntryIDs:             []string{raw},
			ProcessedArtifactID:     raw,
			ReflectionPromptVersion: "reflection-2026.05.0",
			UserResponseKind:        storage.ResponseAccepted,
			UserResponseText:        "Yes, that fits.",
		},
		Body: "I light up after a long run.",
	})
	require.NoError(f.t, err)
	return res.InsightID
}

// foldedRefs reads a person's references on read.
func (f *idFixture) foldedRefs(key string) []string {
	f.t.Helper()
	rec, found, err := f.a.ReadPerson(key)
	require.NoError(f.t, err)
	require.True(f.t, found)
	refs, err := f.a.FoldedEntryRefs(rec)
	require.NoError(f.t, err)
	return refs
}

// heldSamRaws seeds an old "Sam", "Sam Rivera" (whose aka holds "Sam"), and
// n bare "Sam" mentions, each held (§P-11: two active exact holders).
func heldSamRaws(f *idFixture, n int) (oldSam, river string, held []string) {
	f.t.Helper()
	oldSam = f.keyOf(f.structure(m("Sam")))
	river = f.keyOf(f.structure(m("Sam Rivera", "Sam")))
	for range n {
		raw := f.structure(m("Sam"))
		art, err := f.a.ReadProcessed(raw)
		require.NoError(f.t, err)
		require.Empty(f.t, art.People)
		require.Len(f.t, art.HeldPeople, 1, "a bare Sam must be held")
		held = append(held, raw)
	}
	return oldSam, river, held
}

// producedAt matches the produced_at stamp a re-run rewrites.
var producedAt = regexp.MustCompile(`"produced_at": "[^"]*"`)

// ledgerFiles snapshots people/, processed/ (produced_at masked), and the
// identity log, so two re-runs can be proven byte-identical.
func (f *idFixture) ledgerFiles() map[string]string {
	f.t.Helper()
	out := map[string]string{}
	for _, sub := range []string{"people", "processed"} {
		for rel, body := range snapshotTree(f.t, filepath.Join(f.home, sub)) {
			out[sub+"/"+rel] = producedAt.ReplaceAllString(body, `"produced_at": "<masked>"`)
		}
	}
	out["identity/identity.jsonl"] = f.identityBytes()
	return out
}

// TestPersonMoveRef_AppendsCorrectionOnly: `person move-ref --from --to`
// appends exactly one move_ref event naming the raw entry and both canonical
// people, returns its receipt and the documented ack, and leaves every
// people/ and processed/ byte as it was.
func TestPersonMoveRef_AppendsCorrectionOnly(t *testing.T) {
	f := newIDFixture(t)
	raw := f.structure(m("Sam"))
	oldSam := f.keyOf(raw)
	river := f.keyOf(f.structure(m("Sam Rivera")))
	before := f.recordTrees()

	res := f.moveRef(raw, "Sam", "Sam Rivera")
	assert.Equal(t, "identity_2026_11_17_a", res.Receipt)
	assert.Equal(t, river, res.Record.PersonKey, "the receipt's subject is the --to person")
	assert.Equal(t, raw, res.RawID)
	assert.Equal(t, oldSam, res.From)
	assert.Equal(t, river, res.To)
	assert.Empty(t, res.Pinned)
	assert.Zero(t, res.StillHeld)
	assert.Equal(t, "Moved the mention in "+raw+` from "Sam" (`+oldSam+`) to "Sam Rivera" (`+river+`) (identity_2026_11_17_a).`, res.Ack)

	assert.Equal(t, before, f.recordTrees(), "a move-ref writes only the identity log")
	evs := f.events()
	require.Len(t, evs, 1)
	ev := evs[0]
	assert.Equal(t, identity.KindMoveRef, ev.Kind)
	assert.Equal(t, identity.SourceCLI, ev.Source)
	assert.Equal(t, raw, ev.RawID)
	assert.Equal(t, oldSam, ev.From)
	assert.Equal(t, river, ev.To)

	// The undo is the reverse move-ref, appended like any other correction.
	undo := f.moveRef(raw, river, oldSam)
	assert.Equal(t, "identity_2026_11_17_b", undo.Receipt)
	assert.Equal(t, []string{raw}, f.foldedRefs(oldSam))
	assert.NotContains(t, f.foldedRefs(river), raw)
	assert.Equal(t, before, f.recordTrees())
}

// TestPersonMoveRef_RejectionsP12: every move-ref that cannot apply is
// refused with its fixed §P-12 copy, before anything is written — an unknown
// raw id, no mention under --from, --from equal to --to, and no --from on an
// entry with zero or two held mentions; an unknown --to carries the `person
// create` pointer (§P-4).
func TestPersonMoveRef_RejectionsP12(t *testing.T) {
	f := newIDFixture(t)
	oldSam, river, held := heldSamRaws(f, 1)
	samRaw := f.structure(m("Sam Rivera"))
	plain := f.structure(m("Jordan Lee"))
	jordan := f.keyOf(plain)
	// An entry with two held mentions needs a second collision: "Al" is held
	// by "Al" and by "Jordan Lee, aka Al".
	f.structure(m("Al"))
	f.structure(m("Jordan Lee", "Al"))
	two := f.structure(m("Sam"), m("Al"))
	art, err := f.a.ReadProcessed(two)
	require.NoError(t, err)
	require.Len(t, art.HeldPeople, 2, "both mentions of the entry are held")
	logBefore := f.identityBytes()
	treesBefore := f.recordTrees()

	cases := []struct {
		name      string
		raw, from string
		to        string
		cause     error
		want      string
	}{
		{
			name: "unknown raw id", raw: "raw_2026_01_01_00_00", from: oldSam, to: river,
			cause: ErrPersonMoveRefRejected,
			want:  "there is no entry raw_2026_01_01_00_00 — check the raw id; nothing was saved.",
		},
		{
			name: "malformed raw id", raw: "raw_../../people_x_y_z", to: river,
			cause: ErrPersonMoveRefRejected,
			want:  "there is no entry raw_../../people_x_y_z — check the raw id; nothing was saved.",
		},
		{
			name: "no mention under from", raw: plain, from: oldSam, to: river,
			cause: ErrPersonMoveRefRejected,
			want:  plain + ` has no mention under "Sam" (` + oldSam + `) to move; nothing was saved.`,
		},
		{
			name: "from equals to", raw: samRaw, from: "Sam Rivera", to: river,
			cause: ErrPersonMoveRefRejected,
			want:  `--from and --to are the same person ("Sam Rivera"); nothing was saved.`,
		},
		{
			name: "no from and no held mention", raw: plain, to: river,
			cause: ErrPersonMoveRefRejected,
			want:  plain + " has no held mention — name the person it is filed under with --from; nothing was saved.",
		},
		{
			name: "no from and two held mentions", raw: two, to: river,
			cause: ErrPersonMoveRefRejected,
			want:  two + ` has more than one held mention ("Al", "Sam") — settle them with lucid person claim or lucid person dormant; nothing was saved.`,
		},
		{
			name: "unknown to", raw: held[0], to: "Robin Nobody",
			cause: ErrPersonUnknownSubject,
			want:  `no one matches "Robin Nobody" — add them first with lucid person create <name>; nothing was saved.`,
		},
		{
			name: "ambiguous to", raw: plain, from: jordan, to: "Sam",
			cause: ErrPersonAmbiguousSubject,
			want:  `"Sam" matches more than one person — disambiguate with a person_key (`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := f.r.PersonMoveRef(PersonMoveRefRequest{RawID: tc.raw, From: tc.from, To: tc.to, Now: f.now()})
			require.Error(t, err)
			require.ErrorIs(t, err, tc.cause)
			if strings.HasSuffix(tc.want, "(") {
				assert.True(t, strings.HasPrefix(err.Error(), tc.want), err.Error())
			} else {
				assert.Equal(t, tc.want, err.Error())
			}
			assert.True(t, strings.HasSuffix(err.Error(), "nothing was saved."))
			assert.Equal(t, logBefore, f.identityBytes(), "a refusal appends nothing")
			assert.Equal(t, treesBefore, f.recordTrees())
		})
	}
}

// TestPersonMoveRef_PinsPendingWithoutFrom: leaving out --from pins the raw
// entry's one pending held mention to --to with a pin whose cause is move_ref;
// the hold stops being pending and the mention reads under --to.
func TestPersonMoveRef_PinsPendingWithoutFrom(t *testing.T) {
	f := newIDFixture(t)
	oldSam, river, held := heldSamRaws(f, 2)
	before := f.recordTrees()

	res := f.moveRef(held[0], "", "Sam Rivera")
	assert.Equal(t, river, res.Record.PersonKey)
	assert.Equal(t, held[0], res.RawID)
	assert.Empty(t, res.From, "from is left out when --from was")
	assert.Equal(t, river, res.To)
	require.Len(t, res.Pinned, 1)
	assert.Equal(t, storage.PinnedMention{RawID: held[0], Form: "Sam", PersonKey: river}, res.Pinned[0])
	assert.Equal(t, `Pinned the held "Sam" in `+held[0]+` to "Sam Rivera" (`+river+`) (`+res.Receipt+`).`, res.Ack)
	assert.Equal(t, before, f.recordTrees(), "the pin writes only the identity log")

	last := f.lastEvents(1)[0]
	assert.Equal(t, identity.KindPin, last.Kind)
	assert.Equal(t, identity.CauseMoveRef, last.Cause)
	assert.Equal(t, res.Receipt, last.ID)
	assert.Equal(t, river, last.PersonKey)

	st := f.state()
	pending := st.PendingHolds()
	require.Len(t, pending, 1, "only the other entry's hold is still pending")
	assert.Equal(t, held[1], pending[0].RawID)
	assert.Contains(t, f.foldedRefs(river), held[0])
	assert.NotContains(t, f.foldedRefs(oldSam), held[0])
	assert.NotContains(t, f.foldedRefs(river), held[1])

	// The entry has no held mention left, so a second move-ref without --from
	// is refused.
	_, err := f.r.PersonMoveRef(PersonMoveRefRequest{RawID: held[0], To: river, Now: f.now()})
	require.ErrorIs(t, err, ErrPersonMoveRefRejected)
	// The pinned mention now moves only through move-ref --from.
	again := f.moveRef(held[0], river, oldSam)
	assert.NotEmpty(t, again.Receipt)
	assert.Contains(t, f.foldedRefs(oldSam), held[0])
	assert.NotContains(t, f.foldedRefs(river), held[0])
}

// TestPersonMoveRef_FoldsOnRead: after a move-ref every read that attributes
// the entry follows it — the /person mention count, the off-limits entry list,
// the accepted insights citing it, the dominance line, and reconcile's mention
// counts all move from the --from person to the --to person only.
func TestPersonMoveRef_FoldsOnRead(t *testing.T) {
	f := newIDFixture(t)
	first := f.structure(m("Sam"))
	oldSam := f.keyOf(first)
	moved := f.structure(m("Sam"))
	riverRaw := f.structure(m("Sam Rivera"))
	river := f.keyOf(riverRaw)
	insight := f.acceptInsightCiting(moved)

	samView := f.view(oldSam)
	assert.Contains(t, samView, "Mentioned in 2 entries")
	assert.Contains(t, samView, "Referenced by accepted insights: "+insight)
	assert.Contains(t, samView, "Sam appears in 67% of entries — worth a look, or expected?")
	assert.Contains(t, f.view(river), "Mentioned in 1 entry ")
	rec, err := f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	require.Len(t, rec.Candidates, 1)
	assert.Equal(t, river, rec.Candidates[0].SourceKey, "fewer mentions folds into more")

	f.moveRef(moved, oldSam, river)

	samView = f.view(oldSam)
	assert.Contains(t, samView, "Mentioned in 1 entry ")
	assert.Contains(t, samView, "No accepted insights reference them yet.")
	assert.NotContains(t, samView, "worth a look")
	riverView := f.view(river)
	assert.Contains(t, riverView, "Mentioned in 2 entries")
	assert.Contains(t, riverView, "Referenced by accepted insights: "+insight)
	assert.Contains(t, riverView, "Sam Rivera appears in 67% of entries — worth a look, or expected?")
	assert.Equal(t, riverView, f.view(river), "the folded view is byte-stable")

	rec, err = f.r.PersonReconcile(PersonReconcileRequest{})
	require.NoError(t, err)
	require.Len(t, rec.Candidates, 1)
	assert.Equal(t, oldSam, rec.Candidates[0].SourceKey, "the moved mention now counts for Sam Rivera")
	assert.Equal(t, river, rec.Candidates[0].TargetKey)

	// The off-limits raw-record view lists the folded entries.
	_, err = f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: river})
	require.NoError(t, err)
	_, err = f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: oldSam})
	require.NoError(t, err)
	assert.Contains(t, f.view(river), "Entries: "+moved+", "+riverRaw)
	assert.Contains(t, f.view(oldSam), "Entries: "+first)
	assert.NotContains(t, f.view(oldSam), moved)
}

// TestReflectDominance_FoldsMoveRef: the /reflect gate dominance panel counts
// a moved mention for its --to person, by their canonical name, and a mention
// moved onto an off-limits person counts for no one (fail closed).
func TestReflectDominance_FoldsMoveRef(t *testing.T) {
	f := newIDFixture(t)
	oldSam := f.keyOf(f.structure(m("Sam")))
	moved := f.structure(m("Sam"))
	river := f.keyOf(f.structure(m("Sam Rivera")))

	line, err := f.r.dominanceLine()
	require.NoError(t, err)
	assert.Equal(t, "Sam appears in 67% of entries this window — worth a look, or expected?", line)

	f.moveRef(moved, oldSam, river)
	line, err = f.r.dominanceLine()
	require.NoError(t, err)
	assert.Equal(t, "Sam Rivera appears in 67% of entries this window — worth a look, or expected?", line)

	_, err = f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: river})
	require.NoError(t, err)
	line, err = f.r.dominanceLine()
	require.NoError(t, err)
	assert.Empty(t, line, "an off-limits --to is never named; Sam alone is 33%")
}

// TestReflectionSlice_FoldsMoveRefFailClosed: the agent-bound Reflection
// slice attributes each mention on read and redacts it when either its stored
// or its folded key is off-limits — a mention moved onto an off-limits person
// and one moved off an off-limits person are both dropped. A pinned held
// mention enters the slice; an unpinned one never does.
func TestReflectionSlice_FoldsMoveRefFailClosed(t *testing.T) {
	f := newIDFixture(t)
	oldSam, _, held := heldSamRaws(f, 2)
	pinned, unpinned := held[0], held[1]
	ontoHidden := f.structure(m("Sam"))
	jordanRaw := f.structure(m("Jordan Lee"))
	jordan := f.keyOf(jordanRaw)
	offHidden := f.structure(m("Jordan Lee"))
	visible := f.structure(m("Sam Rivera"))
	_, err := f.r.PersonOffLimits(PersonOffLimitsRequest{Subject: jordan})
	require.NoError(t, err)

	// A bare "Sam" here is held too (two exact holders); pin it to Jordan Lee,
	// who is off-limits, so the slice must drop it.
	art, err := f.a.ReadProcessed(ontoHidden)
	require.NoError(t, err)
	require.Len(t, art.HeldPeople, 1)
	f.moveRef(ontoHidden, "", jordan)
	f.moveRef(pinned, "", "Sam Rivera")
	f.moveRef(offHidden, jordan, oldSam)

	current, err := f.a.ReadProcessed(visible)
	require.NoError(t, err)
	in, err := f.r.buildProposeInput(current, false)
	require.NoError(t, err)
	views := map[string]reflection.ProcessedView{in.Current.ID: in.Current}
	for _, v := range in.RecentWindow {
		views[v.ID] = v
	}

	assert.Equal(t, []string{"Sam Rivera"}, views[visible].People, "an untouched visible mention is kept")
	assert.Equal(t, []string{"Sam"}, views[pinned].People, "a pinned held mention enters the slice")
	assert.Empty(t, views[unpinned].People, "an unpinned held mention never enters the slice")
	assert.Empty(t, views[ontoHidden].People, "a mention pinned onto an off-limits person is redacted")
	assert.Empty(t, views[offHidden].People, "a mention moved off an off-limits person is still redacted (stored key)")
	assert.Empty(t, views[jordanRaw].People, "an off-limits mention is redacted")
}

// TestPersonMoveRef_SurvivesStructureRerun: a `lucid structure --force`
// re-run of an entry that carries a move-ref, or a pin, does not undo the
// correction — the mention still reads under the --to person — and two
// consecutive re-runs leave every file byte-identical except produced_at.
func TestPersonMoveRef_SurvivesStructureRerun(t *testing.T) {
	f := newIDFixture(t)
	oldSam, river, held := heldSamRaws(f, 1)
	moved := f.structure(m("Sam Rivera"))
	f.moveRef(moved, river, oldSam)
	f.moveRef(held[0], "", oldSam)

	reads := func() {
		t.Helper()
		samRefs := f.foldedRefs(oldSam)
		assert.Contains(t, samRefs, moved)
		assert.Contains(t, samRefs, held[0])
		riverRefs := f.foldedRefs(river)
		assert.NotContains(t, riverRefs, moved)
		assert.NotContains(t, riverRefs, held[0])
	}
	reads()

	rerun := func() {
		t.Helper()
		f.restructure(moved, m("Sam Rivera"))
		f.restructure(held[0], m("Sam"))
	}
	rerun()
	reads()
	first := f.ledgerFiles()
	rerun()
	reads()
	assert.Equal(t, first, f.ledgerFiles(), "a second re-run changes nothing but produced_at")

	// The re-run kept the move-ref's raw filed under the stored key; only the
	// read moves it.
	assert.Equal(t, river, f.keyOf(moved))
}
