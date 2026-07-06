package validate

// schemaKind pairs a record family with the Ledger-relative directory its
// records live under, so a schema finding points at the right place on disk.
type schemaKind struct {
	dir  string
	list func(LedgerSource) ([]string, error)
	read func(LedgerSource, string) error
}

// schemaKinds enumerates the on-disk record families the schema sweep walks.
// Each family's list op enumerates its ids and its read op parses one record
// through the same validator the writer used (data-model.md): a parse or
// schema failure surfaces as a finding, and the record is never rewritten.
//
//nolint:gochecknoglobals // fixed, read-only schema walk table
var schemaKinds = []schemaKind{
	{"processed", LedgerSource.ListProcessedIDs, LedgerSource.ReadProcessedErr},
	{"insights", LedgerSource.ListInsightIDs, LedgerSource.ReadInsightErr},
	{"reflections", LedgerSource.ListReflectionIDs, LedgerSource.ReadReflectionErr},
	{"people", LedgerSource.ListPeopleKeys, LedgerSource.ReadPersonErr},
}

// CheckLedgerSchema validates every on-disk Ledger record through its
// adapter validator, read-only. lucid.json is checked first, then each record
// family. A record that fails to parse is one error-severity finding; a
// listing that cannot be read at all is returned as an error (the tree is
// unreadable, not merely malformed). An empty or fresh Ledger is clean.
func CheckLedgerSchema(src LedgerSource) ([]Finding, error) {
	var findings []Finding

	if err := src.LoadConfigErr(); err != nil {
		findings = append(findings, Finding{
			Check:    CheckSchema,
			Severity: SeverityError,
			Path:     "lucid.json",
			Rule:     "config",
			Message:  err.Error(),
		})
	}

	for _, k := range schemaKinds {
		ids, err := k.list(src)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			if rerr := k.read(src, id); rerr != nil {
				findings = append(findings, Finding{
					Check:    CheckSchema,
					Severity: SeverityError,
					Path:     k.dir + "/" + id,
					Rule:     k.dir,
					Message:  rerr.Error(),
				})
			}
		}
	}
	return findings, nil
}
