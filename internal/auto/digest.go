package auto

import (
	"strings"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/ledger"
	"github.com/JtheGunner/ticktack/internal/resolver"
	"github.com/JtheGunner/ticktack/internal/sessionizer"
)

// DigestEntry is one line of the digest. Status is the ledger action
// (created, updated, recovered, deleted, skipped, failed, not-sent, or a
// dry-run plan).
type DigestEntry struct {
	Ticket  string
	Date    civil.Date
	Minutes int
	Tier    resolver.Tier
	Status  string
	Reason  string
}

// Digest summarises one auto run: every item that was booked, updated,
// deleted or skipped, with totals.
type Digest struct {
	Day     civil.Date
	DryRun  bool
	Entries []DigestEntry

	Created, Updated, Recovered, Deleted, Skipped, Failed, NotSent int
	// BookedMinutes is the sum of minutes of created, updated and recovered items.
	BookedMinutes int
	CapExceeded   bool
	TotalMinutes  int
	CapMinutes    int
}

// Changed reports whether the run created, updated, recovered or deleted
// anything, so that "ticktack undo" has something to revert.
func (d Digest) Changed() bool { return d.Created+d.Updated+d.Recovered+d.Deleted > 0 }

// BuildDigest merges the selection with the ledger report. Every ledger
// outcome becomes an entry with the tier of its item; every item the
// selection skipped is listed with its reason. A booking the run deleted has
// no item any more, so its entry carries the ticket of its key and no tier.
func BuildDigest(day civil.Date, sel Selection, report ledger.Report, dryRun bool) Digest {
	d := Digest{
		Day: day, DryRun: dryRun,
		Created: report.Created, Updated: report.Updated, Recovered: report.Recovered, Deleted: report.Deleted,
		Skipped: report.Skipped + len(sel.Skipped), Failed: report.Failed, NotSent: report.NotSent,
		CapExceeded: sel.CapExceeded, TotalMinutes: sel.TotalMinutes, CapMinutes: sel.CapMinutes,
	}
	booked := map[string]Entry{}
	for _, e := range sel.Book {
		booked[key(e.Item)] = e
	}
	for _, r := range report.Items {
		entry := DigestEntry{Minutes: r.Minutes, Status: string(r.Action), Reason: r.Reason}
		if e, ok := booked[r.Key]; ok {
			entry.Ticket, entry.Date, entry.Tier = e.Item.Ticket, e.Item.Date, e.Tier
		} else {
			entry.Ticket, _, _ = strings.Cut(r.Key, ":"+day.String()+":")
			entry.Date = day
		}
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
