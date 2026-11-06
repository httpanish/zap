package engine

// Phase is a stage of the test sequence.
type Phase int

// The phases, in the order they run.
const (
	PhaseConnect Phase = iota
	PhaseIdle
	PhaseDownload
	PhaseUpload
)

func (p Phase) String() string {
	switch p {
	case PhaseConnect:
		return "connect"
	case PhaseIdle:
		return "idle latency"
	case PhaseDownload:
		return "download"
	case PhaseUpload:
		return "upload"
	}
	return "unknown"
}

// Event is something the engine reports to its consumer. Progress events are
// lossy; every other event is guaranteed to arrive. The channel returned by
// Start must be drained until it closes, or the engine blocks on a guaranteed
// event.
type Event interface{ isEvent() }

// PhaseStarted reports that a phase has begun.
type PhaseStarted struct{ Phase Phase }

// Connected reports the metadata from the connect phase.
type Connected struct{ Server Server }

// IdleDone reports the idle latency figures.
type IdleDone struct {
	IdleMs   float64
	JitterMs float64
}

// Progress is a lossy update during a phase. Each event carries the whole state
// so far, so a dropped event loses nothing.
type Progress struct {
	Phase      Phase
	Fraction   float64   // 0 to 1, how far through the phase.
	Mbps       float64   // Current throughput, smoothed over about 1 second.
	Series     []float64 // Throughput per sampling interval so far.
	LoadedMs   *float64  // Median so far of the post-ramp-up probes, if any.
	TotalBytes int64     // Payload bytes so far across both transfer phases.
}

// TransferDone reports the final figures for a transfer phase.
type TransferDone struct {
	Phase            Phase
	Mbps             float64
	Series           []float64
	LoadedMs         *float64
	LoadedLowerBound bool
	TotalBytes       int64
}

// Finished is always the last event. Exactly one of Result and Err is set.
type Finished struct {
	Result *Result
	Err    error
}

func (PhaseStarted) isEvent() {}
func (Connected) isEvent()    {}
func (IdleDone) isEvent()     {}
func (Progress) isEvent()     {}
func (TransferDone) isEvent() {}
func (Finished) isEvent()     {}
