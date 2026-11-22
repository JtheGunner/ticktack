package sessionizer

import (
	"sort"
	"strings"
	"time"

	"github.com/JtheGunner/ticktack/internal/resolver"
)

// Certainty splits the minutes of a DayItem by how sure the attribution of
// that time is. Explicit, Rule, Learned, Suggested and Unknown add up to the
// item's Minutes. Carried is not a further share: it is the part of Suggested
// that rests on context carried over beyond the age limit (see
// StaleAttribution), so Carried is never greater than Suggested.
type Certainty struct {
	Explicit  int
	Rule      int
	Learned   int
	Suggested int
	Unknown   int
	// Carried is the part of Suggested that is stale carried-over context.
	Carried int
}

// Total returns the minutes over all tiers.
func (c Certainty) Total() int {
	return c.Explicit + c.Rule + c.Learned + c.Suggested + c.Unknown
}

// Add returns the field-wise sum of c and o.
func (c Certainty) Add(o Certainty) Certainty {
	return Certainty{
		Explicit:  c.Explicit + o.Explicit,
		Rule:      c.Rule + o.Rule,
		Learned:   c.Learned + o.Learned,
		Suggested: c.Suggested + o.Suggested,
		Unknown:   c.Unknown + o.Unknown,
		Carried:   c.Carried + o.Carried,
	}
}

// Sure returns the minutes on tiers auto mode may book: explicit and rule.
func (c Certainty) Sure() int { return c.Explicit + c.Rule }

// certaintyBucket is a disjoint class of time. Suggested time is split into
// fresh and carried so that both can be rounded independently; the buckets
// are ordered from most to least certain.
type certaintyBucket int

const (
	bucketExplicit certaintyBucket = iota
	bucketRule
	bucketLearned
	bucketSuggested
	bucketCarried
	bucketUnknown
	bucketCount
)

// bucketOf classifies a block. Only a suggested block whose reason is the
// stale-context reason counts as carried.
func bucketOf(b Block) certaintyBucket {
	switch b.Tier {
	case resolver.TierExplicit:
		return bucketExplicit
	case resolver.TierRule:
		return bucketRule
	case resolver.TierLearned:
		return bucketLearned
	case resolver.TierSuggested:
		if strings.HasPrefix(b.Reason, staleReasonPrefix) {
			return bucketCarried
		}
		return bucketSuggested
	}
	return bucketUnknown
}

// apportionCertainty distributes the rounded units of one item over its
// buckets. The rule is explicit: every bucket first gets the whole units of
// its own duration; the remaining units go one by one to the buckets with the
// largest remainder, ties to the less certain bucket. The result therefore
// always adds up to units*step, and a bucket never differs from its exact
// share by a whole unit or more.
func apportionCertainty(durations [bucketCount]time.Duration, units int64, step time.Duration) Certainty {
	var share [bucketCount]int64
	var assigned int64
	for i, d := range durations {
		share[i] = int64(d / step)
		assigned += share[i]
	}
	order := make([]int, 0, bucketCount)
	for i := int(bucketCount) - 1; i >= 0; i-- {
		if durations[i] > 0 {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return durations[order[a]]%step > durations[order[b]]%step
	})
	for i := 0; assigned < units && len(order) > 0; i++ {
		share[order[i%len(order)]]++
		assigned++
	}

	minutes := func(b certaintyBucket) int { return int(time.Duration(share[b]) * step / time.Minute) }
	return Certainty{
		Explicit:  minutes(bucketExplicit),
		Rule:      minutes(bucketRule),
		Learned:   minutes(bucketLearned),
		Suggested: minutes(bucketSuggested) + minutes(bucketCarried),
		Unknown:   minutes(bucketUnknown),
		Carried:   minutes(bucketCarried),
	}
}
