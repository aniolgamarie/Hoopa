package config

import (
	"math"
	"time"
	"unicode/utf8"
)

// Validate applies the v1 Apply constraints to a typed probe without performing
// any network operation. It also protects callers constructing Probe directly.
func (p Probe) Validate() error {
	if !utf8.ValidString(p.URL) || !validProbeURL(p.URL) || p.IntervalSeconds == 0 || p.TimeoutMS == 0 ||
		p.IntervalSeconds > maxIntentInteger || p.TimeoutMS > maxIntentInteger || p.ToleranceMS > maxIntentInteger ||
		p.IntervalSeconds > uint64(math.MaxInt64/int64(time.Second)) ||
		p.TimeoutMS > uint64(math.MaxInt64/int64(time.Millisecond)) ||
		p.ToleranceMS > uint64(math.MaxInt64/int64(time.Millisecond)) {
		return ErrIntent
	}
	return nil
}
