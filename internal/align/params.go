package align

// Recipe constants of the alignment core (docs/read-along.md section 2). Changing one is a new recipe
// version (RecipeAlign), so every alignment is made again.
const (
	WindowMs       = 30000   // emission window, central part kept
	OverlapMs      = 1000    // overlap each side of a window
	FrameMs        = 20      // one model frame
	QStep          = 0.1     // nats per quantisation step of the emissions cache
	BlockMs        = 600000  // coarse-map block length
	PauseFrames    = 15      // blank run that counts as a pause (300 ms)
	MinHits        = 3       // 5-gram hits a region needs
	MaxRegions     = 4       // text regions one block may map to
	SegMaxMs       = 30000   // longest audio segment given to one Viterbi run
	PadFrames      = 10      // padding around a segment (200 ms)
	RateMin        = 0.5     // words per second, lower bound for a forced segment
	RateMax        = 6.0     // words per second, upper bound for a forced segment
	NWMaxCells     = 4000000 // Needleman-Wunsch cell bound per gap
	TimedMin       = 0.30    // a word is timed from this duration-weighted token probability
	High           = 0.75    // sentence confidence at or above is high
	Low            = 0.40    // sentence confidence under is low
	DoneMin        = 0.90    // body coverage for state done
	PartialMin     = 0.05    // body coverage for state partial (under it: failed, no_match)
	AbridgedSpeech = 0.85    // share of speech mapped for the abridged label
	AbridgedText   = 0.70    // share of text timed under which the abridged label shows
	EditionNear    = 0.08    // differing share inside anchored regions for different_edition
)

// Labels on an alignment.
const (
	LabelAbridged         = "abridged"
	LabelDifferentEdition = "different_edition"
)

// CodeNoMatch is the error code when under PartialMin of the body is timed.
const CodeNoMatch = "no_match"

// Gap reasons of a sentence with no time.
const (
	GapNotInRecording = "not-in-recording"
	GapNotFound       = "not-found"
	GapUnsupported    = "unsupported"
)

// Params carries the recipe constants so a test can shrink BlockMs. Use DefaultParams.
type Params struct {
	BlockMs        int64
	PauseFrames    int
	MinHits        int
	MaxRegions     int
	SegMaxMs       int64
	PadFrames      int
	RateMin        float64
	RateMax        float64
	NWMaxCells     int
	TimedMin       float64
	DoneMin        float64
	PartialMin     float64
	AbridgedSpeech float64
	AbridgedText   float64
	EditionNear    float64
}

// DefaultParams returns the recipe values.
func DefaultParams() Params {
	return Params{
		BlockMs: BlockMs, PauseFrames: PauseFrames, MinHits: MinHits, MaxRegions: MaxRegions,
		SegMaxMs: SegMaxMs, PadFrames: PadFrames, RateMin: RateMin, RateMax: RateMax,
		NWMaxCells: NWMaxCells, TimedMin: TimedMin, DoneMin: DoneMin, PartialMin: PartialMin,
		AbridgedSpeech: AbridgedSpeech, AbridgedText: AbridgedText, EditionNear: EditionNear,
	}
}

// Level names a sentence confidence: "high", "mid" or "low".
func Level(conf float64) string {
	switch {
	case conf >= High:
		return "high"
	case conf < Low:
		return "low"
	}
	return "mid"
}
