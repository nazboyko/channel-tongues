package youtube

import (
	"time"
	_ "time/tzdata" // the Pacific day must work on machines without zoneinfo

	"github.com/nazboyko/channel-tongues/internal/store"
)

// API costs in quota units, from the YouTube Data API reference.
const (
	CostRead    = 1                       // videos.list, channels.list, playlistItems.list, i18nLanguages.list
	CostUpdate  = 50                      // videos.update
	CostPublish = CostUpdate + 3*CostRead // read, re-read just before writing, update, read back

	// DailyLimit is the default free quota; DailyBudget leaves room for reads.
	DailyLimit  = 10000
	DailyBudget = 9000
)

var pacific = mustLocation("America/Los_Angeles")

func mustLocation(name string) *time.Location {
	loc, err := time.LoadLocation(name)
	if err != nil {
		panic(err)
	}
	return loc
}

// PacificDay is the quota day: YouTube resets quotas at midnight Pacific time.
func PacificDay(t time.Time) string {
	return t.In(pacific).Format("2006-01-02")
}

// NextReset is the next Pacific midnight after t.
func NextReset(t time.Time) time.Time {
	p := t.In(pacific)
	return time.Date(p.Year(), p.Month(), p.Day()+1, 0, 0, 0, 0, pacific)
}

// Ledger records the API cost of every call on the Pacific day.
type Ledger struct {
	Store *store.Store
	Now   func() time.Time
}

func (l *Ledger) now() time.Time {
	if l.Now != nil {
		return l.Now()
	}
	return time.Now()
}

// Spend records units on today's Pacific day. A nil ledger records nothing.
func (l *Ledger) Spend(units int) error {
	if l == nil || l.Store == nil {
		return nil
	}
	return l.Store.AddUnits(PacificDay(l.now()), units)
}

// UsedToday is the cost recorded on today's Pacific day.
func (l *Ledger) UsedToday() (int, error) {
	if l == nil || l.Store == nil {
		return 0, nil
	}
	return l.Store.Units(PacificDay(l.now()))
}

// VideosLeftToday is how many more videos can be published today within the budget.
func (l *Ledger) VideosLeftToday() (int, error) {
	used, err := l.UsedToday()
	if err != nil {
		return 0, err
	}
	left := (DailyBudget - used) / CostPublish
	if left < 0 {
		left = 0
	}
	return left, nil
}
