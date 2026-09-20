package browsertool

// PageChange compares bounded DOM observations, not business outcomes.
type PageChange struct {
	Observed bool     `json:"observed"`
	Added    []string `json:"added"`
	Removed  []string `json:"removed"`
	Omitted  bool     `json:"omitted,omitempty"`
}

type PageProgress struct {
	UnchangedActions int    `json:"unchanged_actions"`
	Note             string `json:"note"`
}
