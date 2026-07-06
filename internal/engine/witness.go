package engine

// Witness contract lifecycle states (engine-module.md §witness.json
// `status_history[]`). Every transition is appended, never overwritten (the
// registries precedent): a witness is briefed, then confirmed; a resignation
// or sustained unreachability lapses the contract and disarms L2.
const (
	WitnessBriefed   = "briefed"
	WitnessConfirmed = "confirmed"
	WitnessLapsedS   = "lapsed"
	WitnessResigned  = "resigned"
)

// WitnessChannel is the one dedicated channel the witness reads
// (engine-module.md §witness.json). The witness's access is scoped by
// explicit channel permissions to this channel only — #lucid and its threads,
// where journal lines and observation micro-logs are typed, stay invisible to
// the witness role. The Engine never grants the witness any access to
// ~/.lucid/; they see only what the L2 template carries.
type WitnessChannel struct {
	Kind string `json:"kind"`
	ID   string `json:"id"`
}

// WitnessTransition is one append-only entry in the witness contract's
// status_history (engine-module.md §witness.json "Lifecycle": transitions are
// recorded, never overwritten).
type WitnessTransition struct {
	At     string `json:"at"`
	Status string `json:"status"`
}

// WitnessContract is witness.json (engine-module.md §witness.json): the
// witness identity, their confirmed channel, the consent record, and the
// append-only lifecycle history. confirmed_at is null until the witness's own
// confirmation message is recorded; l2_enabled cannot be true before then.
//
// The optional identity fields carry omitempty so the fresh scaffold stub
// (`{"confirmed_at": null, "l2_enabled": false, "status_history": []}`) and a
// fully-provisioned contract both round-trip through this type.
type WitnessContract struct {
	WitnessName      string              `json:"witness_name,omitempty"`
	Channel          *WitnessChannel     `json:"channel,omitempty"`
	BriefedAt        string              `json:"briefed_at,omitempty"`
	ConfirmedAt      *string             `json:"confirmed_at"`
	ConfirmationText string              `json:"confirmation_text,omitempty"`
	Sees             []string            `json:"sees,omitempty"`
	StakeShared      bool                `json:"stake_shared,omitempty"`
	L2Enabled        bool                `json:"l2_enabled"`
	StatusHistory    []WitnessTransition `json:"status_history"`
}

// IsConfirmed reports whether the witness has recorded a confirmation —
// confirmed_at is set and non-empty (engine-module.md §witness.json: "until
// confirmed_at is set, l2_enabled cannot be turned on").
func (w WitnessContract) IsConfirmed() bool {
	return w.ConfirmedAt != nil && *w.ConfirmedAt != ""
}

// LatestStatus returns the most recent lifecycle status, or "" when the
// history is empty (a never-provisioned witness).
func (w WitnessContract) LatestStatus() string {
	if n := len(w.StatusHistory); n > 0 {
		return w.StatusHistory[n-1].Status
	}
	return ""
}

// IsLapsed reports whether the contract is in witness-lapsed state — the
// witness resigned or went sustainedly unreachable (engine-module.md
// §witness.json "Lifecycle"). A never-provisioned witness is not lapsed; it is
// simply unset. While lapsed, the ladder degrades to L1-only and /status
// surfaces "witness lapsed — L2 disarmed".
func (w WitnessContract) IsLapsed() bool {
	switch w.LatestStatus() {
	case WitnessLapsedS, WitnessResigned:
		return true
	default:
		return false
	}
}

// L2Armed reports whether an L2 escalation may actually be delivered to the
// witness channel: the witness is confirmed, l2_enabled is set, and the
// contract is not lapsed (engine-module.md §Consent amendment: "L2 cannot be
// enabled until witness.json has confirmed_at"; §witness.json "Lifecycle":
// lapsed degrades to L1-only). When this is false the tripwire's L2 stage is
// blocked and the user is notified instead — the escalation still fires, only
// its delivery to the witness is withheld.
func (w WitnessContract) L2Armed() bool {
	return w.IsConfirmed() && w.L2Enabled && !w.IsLapsed()
}
