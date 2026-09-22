package browsertool

import "context"

// ObservationProfile selects a server-owned browser observation budget. The
// model cannot raise these limits through tool arguments.
type ObservationProfile uint8

const (
	ObservationDefault ObservationProfile = iota
	// ObservationResearch keeps enough article text and controls for source
	// verification while avoiding large navigation/catalog snapshots on every
	// model turn.
	ObservationResearch
	// ObservationDetailed retains the legacy maximums for pages where a caller
	// has explicitly decided that a larger observation is worth the token cost.
	ObservationDetailed
)

var observationProfiles = map[ObservationProfile]observationLimits{
	ObservationDefault:  {textChars: 4000, elementRefs: 40, outputBytes: 10 << 10},
	ObservationResearch: {textChars: 6000, elementRefs: 48, outputBytes: 12 << 10},
	ObservationDetailed: {textChars: MaxSnapshotText, elementRefs: MaxSnapshotRefs, outputBytes: 60000},
}

type observationLimits struct {
	textChars   int
	elementRefs int
	outputBytes int
}

type observationProfileKey struct{}

// WithObservationProfile attaches a browser output policy to one run.
func WithObservationProfile(ctx context.Context, profile ObservationProfile) context.Context {
	return context.WithValue(ctx, observationProfileKey{}, profile)
}

func observationLimitsFrom(ctx context.Context) observationLimits {
	profile, _ := ctx.Value(observationProfileKey{}).(ObservationProfile)
	limits, ok := observationProfiles[profile]
	if !ok {
		limits = observationProfiles[ObservationDefault]
	}
	return limits
}

func (l observationLimits) normalized() observationLimits {
	defaults := observationProfiles[ObservationDefault]
	if l.textChars <= 0 {
		l.textChars = defaults.textChars
	} else if l.textChars > MaxSnapshotText {
		l.textChars = MaxSnapshotText
	}
	if l.elementRefs <= 0 {
		l.elementRefs = defaults.elementRefs
	} else if l.elementRefs > MaxSnapshotRefs {
		l.elementRefs = MaxSnapshotRefs
	}
	if l.outputBytes <= 0 {
		l.outputBytes = defaults.outputBytes
	} else if l.outputBytes > 60000 {
		l.outputBytes = 60000
	}
	return l
}
