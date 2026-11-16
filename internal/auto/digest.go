package auto

import (
	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// DigestEntry is one line of the digest. Status is the ledger action
// (created, updated, recovered, skipped, failed, not-sent, or a dry-run plan).
type DigestEntry struct {
	Ticket  string
	Date    civil.Date
	Minutes int
	Tier    resolver.Tier
	Status  string
	Reason  string
}

// Digest summarises one auto run: every item that was booked, updated or
// skipped, with totals.
type Digest struct {
	Day     civil.Date
	DryRun  bool
	Entries []DigestEntry

	Created, Updated, Recovered, Skipped, Failed, NotSent int
	// BookedMinutes is the sum of minutes of created, updated and recovered items.
	BookedMinutes int
	CapExceeded   bool
	TotalMinutes  int
	CapMinutes    int
}

// Changed reports whether the run created, updated or recovered anything, so
// that "ticktack undo" has something to revert.
func (d Digest) Changed() bool { return d.Created+d.Updated+d.Recovered > 0 }

// BuildDigest merges the selection with the ledger report. Every ledger
// outcome becomes an entry with the tier of its item; every item the
// selection skipped is listed with its reason.
func BuildDigest(day civil.Date, sel Selection, report ledger.Report, dryRun bool) Digest {
	d := Digest{
		Day: day, DryRun: dryRun,
		Created: report.Created, Updated: report.Updated, Recovered: report.Recovered,
		Skipped: report.Skipped + len(sel.Skipped), Failed: report.Failed, NotSent: report.NotSent,
		CapExceeded: sel.CapExceeded, TotalMinutes: sel.TotalMinutes, CapMinutes: sel.CapMinutes,
	}
	booked := map[string]Entry{}
	for _, e := range sel.Book {
		booked[key(e.Item)] = e
	}
	for _, r := range report.Items {
		e := booked[r.Key]
		entry := DigestEntry{Ticket: e.Item.Ticket, Date: e.Item.Date, Minutes: r.Minutes, Tier: e.Tier, Status: string(r.Action), Reason: r.Reason}
		if r.Error != "" {
			entry.Reason = r.Error
		}
		d.Entries = append(d.Entries, entry)
		switch r.Action {
		case ledger.ActionCreated, ledger.ActionUpdated, ledger.ActionRecovered:
			d.BookedMinutes += r.Minutes
		}
	}
	for _, e := range sel.Skipped {
		d.Entries = append(d.Entries, DigestEntry{
			Ticket: e.Item.Ticket, Date: e.Item.Date, Minutes: e.Item.Minutes,
			Tier: e.Tier, Status: string(ledger.ActionSkipped), Reason: e.Reason,
		})
	}
	return d
}

func key(it sessionizer.DayItem) string {
	typ := it.Type
	if typ == "" {
		typ = sessionizer.DefaultWorkItemType
	}
	return ledger.Key(it.Ticket, it.Date.String(), typ)
}
