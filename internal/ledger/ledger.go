// Package ledger books day items idempotently: one work item per
// (issue, date, type), tracked in a local ledger. Every item text carries a
// [ticktack:<key>] marker so items can be found again after the ledger is lost.
package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
	"github.com/JtheGunner/ticktack/internal/youtrack"
)

// Repository is the persistence the ledger needs. *store.Store implements it.
type Repository interface {
	LedgerGet(ctx context.Context, issue, date, typ string) (store.LedgerRow, bool, error)
	BeginSyncRun(ctx context.Context, startedAt time.Time) (int64, error)
	RecordBooking(ctx context.Context, runID int64, row store.LedgerRow, e store.SyncEntry) error
	LastSyncRun(ctx context.Context) (store.SyncRun, bool, error)
	RevertEntry(ctx context.Context, runID int64, e store.SyncEntry, restore *store.LedgerRow) error
}

// Action describes what happened to one item.
type Action string

// Actions reported per item.
const (
	ActionCreated        Action = "created"
	ActionUpdated        Action = "updated"
	ActionSkipped        Action = "skipped"
	ActionRecovered      Action = "recovered"
	ActionFailed         Action = "failed"
	ActionNotSent        Action = "not-sent"
	ActionWouldCreate    Action = "dry-run-would-create"
	ActionWouldUpdate    Action = "dry-run-would-update"
	ActionWouldRecover   Action = "dry-run-would-recover"
	skipReasonNoTicket          = "no ticket"
	skipReasonUnchanged         = "unchanged"
	entryActionCreated          = "created"
	entryActionUpdated          = "updated"
	entryActionRecovered        = "recovered"
	notSentReason               = "not sent: run stopped"
	markerPrefix                = "[ticktack:"
)

// SyncOptions configures Sync.
type SyncOptions struct {
	// Repo is the ledger storage.
	Repo Repository
	// DryRun performs read calls only: no ledger writes and no mutating sink call.
	DryRun bool
	// Now returns the current time; it defaults to time.Now.
	Now func() time.Time
}

// ItemResult is the outcome for one item.
type ItemResult struct {
	Key     string
	Action  Action
	Minutes int
	// Reason explains skipped items; Error carries the failure text.
	Reason string
	Error  string
}

// Report lists every item outcome plus totals.
type Report struct {
	Items     []ItemResult
	Created   int
	Updated   int
	Recovered int
	Skipped   int
	Failed    int
	NotSent   int
	// Planned counts items a dry run would change.
	Planned int
}

func (r *Report) add(res ItemResult) {
	r.Items = append(r.Items, res)
	switch res.Action {
	case ActionCreated:
		r.Created++
	case ActionUpdated:
		r.Updated++
	case ActionRecovered:
		r.Recovered++
	case ActionSkipped:
		r.Skipped++
	case ActionFailed:
		r.Failed++
	case ActionNotSent:
		r.NotSent++
	case ActionWouldCreate, ActionWouldUpdate, ActionWouldRecover:
		r.Planned++
	}
}

// Key returns the identity of a day item: <issue>:<yyyy-mm-dd>:<type>.
func Key(issue, date, typ string) string { return issue + ":" + date + ":" + typ }

// Marker returns the bracketed marker for key.
func Marker(key string) string { return markerPrefix + key + "]" }

// Sync books items through s. Items are processed in date, issue, type order.
// Unauthorized and rate-limit errors stop the run: the remaining items are
// reported as not sent and Sync returns the report together with the error.
// Other failures are recorded per item and the run continues.
func Sync(ctx context.Context, items []sessionizer.DayItem, s sink.TimeSink, opts SyncOptions) (Report, error) {
	if opts.Repo == nil {
		return Report{}, errors.New("ledger: no repository configured")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	sorted := append([]sessionizer.DayItem(nil), items...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Date != b.Date {
			return a.Date.Before(b.Date)
		}
		if a.Ticket != b.Ticket {
			return a.Ticket < b.Ticket
		}
		return itemType(a) < itemType(b)
	})

	run := &syncRun{sink: s, opts: opts}
	var report Report
	for i, it := range sorted {
		if err := ctx.Err(); err != nil {
			markNotSent(&report, sorted[i:])
			return report, err
		}
		res, err := run.syncItem(ctx, it)
		report.add(res)
		if isStopError(err) {
			markNotSent(&report, sorted[i+1:])
			return report, err
		}
	}
	return report, nil
}

func markNotSent(r *Report, rest []sessionizer.DayItem) {
	for _, it := range rest {
		if it.Ticket == "" {
			r.add(skippedNoTicket(it))
			continue
		}
		r.add(ItemResult{Key: itemKey(it), Action: ActionNotSent, Minutes: it.Minutes, Reason: notSentReason})
	}
}

func skippedNoTicket(it sessionizer.DayItem) ItemResult {
	return ItemResult{Key: Key("", it.Date.String(), itemType(it)), Action: ActionSkipped, Minutes: it.Minutes, Reason: skipReasonNoTicket}
}

func itemType(it sessionizer.DayItem) string {
	if it.Type == "" {
		return sessionizer.DefaultWorkItemType
	}
	return it.Type
}

func itemKey(it sessionizer.DayItem) string {
	return Key(it.Ticket, it.Date.String(), itemType(it))
}

func isStopError(err error) bool {
	return errors.Is(err, youtrack.ErrUnauthorized) || errors.Is(err, youtrack.ErrRateLimited)
}

// isClientError reports errors after which Create certainly stored nothing or
// retrying the lookup is pointless (rate limits stop the run).
func isClientError(err error) bool {
	for _, target := range []error{
		youtrack.ErrUnauthorized, youtrack.ErrForbidden, youtrack.ErrIssueNotFound,
		youtrack.ErrTimeTrackingDisabled, youtrack.ErrWorkItemTypeNotFound, youtrack.ErrRateLimited,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	return false
}

type syncRun struct {
	sink  sink.TimeSink
	opts  SyncOptions
	runID int64
}

func (r *syncRun) syncItem(ctx context.Context, it sessionizer.DayItem) (ItemResult, error) {
	if it.Ticket == "" {
		return skippedNoTicket(it), nil
	}
	date := it.Date.String()
	typ := itemType(it)
	key := Key(it.Ticket, date, typ)
	res := ItemResult{Key: key, Minutes: it.Minutes}
	fail := func(err error) (ItemResult, error) {
		res.Action = ActionFailed
		res.Error = err.Error()
		return res, err
	}
	// A per-item failure must not stop the run; stop errors are returned.
	failItem := func(err error) (ItemResult, error) {
		res, err := fail(err)
		if isStopError(err) {
			return res, err
		}
		return res, nil
	}

	work := sink.WorkItem{
		Issue: it.Ticket, Date: it.Date, Minutes: it.Minutes, Type: typ,
		Text: it.Description + " " + Marker(key),
	}
	row, inLedger, err := r.opts.Repo.LedgerGet(ctx, it.Ticket, date, typ)
	if err != nil {
		return failItem(err)
	}

	if inLedger {
		if row.Minutes == it.Minutes {
			res.Action, res.Reason = ActionSkipped, skipReasonUnchanged
			return res, nil
		}
		return r.update(ctx, work, row, res, ActionUpdated, ActionWouldUpdate, entryActionUpdated, row.Minutes)
	}

	id, found, err := r.sink.FindByMarker(ctx, it.Ticket, Marker(key))
	if err != nil {
		return failItem(err)
	}
	if found {
		// The sink does not report minutes, so a recovered item is always
		// rewritten to make sure it carries the current total.
		row = store.LedgerRow{Issue: it.Ticket, Date: date, Type: typ, WorkItemID: id}
		return r.update(ctx, work, row, res, ActionRecovered, ActionWouldRecover, entryActionRecovered, 0)
	}
	if r.opts.DryRun {
		res.Action = ActionWouldCreate
		return res, nil
	}

	id, err = r.sink.Create(ctx, work)
	action, entryAction := ActionCreated, entryActionCreated
	if err != nil {
		if isClientError(err) {
			return failItem(err)
		}
		// The client retries POSTs on 5xx, so the server may have stored the
		// item despite the error: look it up before giving up.
		recovered, ok, findErr := r.sink.FindByMarker(ctx, it.Ticket, Marker(key))
		if findErr != nil || !ok {
			return failItem(err)
		}
		id = recovered
		// Every item found by marker is recorded as recovered, never as
		// created: undo must not delete an item that may predate this run.
		action, entryAction = ActionRecovered, entryActionRecovered
	}
	if err := r.record(ctx, work, key, id, entryAction, 0); err != nil {
		return failItem(err)
	}
	res.Action = action
	return res, nil
}

func (r *syncRun) update(ctx context.Context, work sink.WorkItem, row store.LedgerRow, res ItemResult,
	done, planned Action, entryAction string, previous int) (ItemResult, error) {
	if r.opts.DryRun {
		res.Action = planned
		return res, nil
	}
	if err := r.sink.Update(ctx, row.WorkItemID, work); err != nil {
		res.Action, res.Error = ActionFailed, err.Error()
		if isStopError(err) {
			return res, err
		}
		return res, nil
	}
	if err := r.record(ctx, work, res.Key, row.WorkItemID, entryAction, previous); err != nil {
		res.Action, res.Error = ActionFailed, err.Error()
		return res, nil
	}
	res.Action = done
	return res, nil
}

// record writes the ledger row and the run entry after a successful sink call.
func (r *syncRun) record(ctx context.Context, w sink.WorkItem, key, id, entryAction string, previous int) error {
	now := r.opts.Now()
	if r.runID == 0 {
		runID, err := r.opts.Repo.BeginSyncRun(ctx, now)
		if err != nil {
			return err
		}
		r.runID = runID
	}
	date := w.Date.String()
	row := store.LedgerRow{
		Issue: w.Issue, Date: date, Type: w.Type, WorkItemID: id,
		Minutes: w.Minutes, ContentHash: contentHash(key, w.Minutes, w.Text), BookedAt: now,
	}
	entry := store.SyncEntry{
		Issue: w.Issue, Date: date, Type: w.Type, Action: entryAction, WorkItemID: id,
		PreviousMinutes: previous, NewMinutes: w.Minutes, Text: w.Text,
	}
	return r.opts.Repo.RecordBooking(ctx, r.runID, row, entry)
}

func contentHash(key string, minutes int, text string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", key, minutes, text)))
	return hex.EncodeToString(sum[:])
}

// UndoResult reports what Undo did.
type UndoResult struct {
	// Found is false when there was no sync run to undo.
	Found     bool
	Deleted   int
	Restored  int
	Forgotten int
}

// Undo reverts the last sync run only: created items are deleted from the
// sink and removed from the ledger, updated items are restored to their
// previous minutes. Recovered items stay in the sink (their earlier state is
// unknown) and are only dropped from the ledger. An entry is cleared only after
// its sink call succeeded, so a failed undo can be retried.
func Undo(ctx context.Context, s sink.TimeSink, repo Repository, now func() time.Time) (UndoResult, error) {
	run, found, err := repo.LastSyncRun(ctx)
	if err != nil {
		return UndoResult{}, err
	}
	if !found {
		return UndoResult{}, nil
	}
	result := UndoResult{Found: true}
	var errs []error
	for _, e := range run.Entries {
		date, err := civil.ParseDate(e.Date)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: invalid date %q", Key(e.Issue, e.Date, e.Type), e.Date))
			continue
		}
		var restore *store.LedgerRow
		switch e.Action {
		case entryActionCreated:
			if err := s.Delete(ctx, e.WorkItemID); err != nil && !errors.Is(err, youtrack.ErrIssueNotFound) {
				errs = append(errs, fmt.Errorf("%s: %w", Key(e.Issue, e.Date, e.Type), err))
				if isStopError(err) {
					return result, errors.Join(errs...)
				}
				continue
			}
			result.Deleted++
		case entryActionUpdated:
			w := sink.WorkItem{Issue: e.Issue, Date: date, Minutes: e.PreviousMinutes, Type: e.Type, Text: e.Text}
			if err := s.Update(ctx, e.WorkItemID, w); err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", Key(e.Issue, e.Date, e.Type), err))
				if isStopError(err) {
					return result, errors.Join(errs...)
				}
				continue
			}
			restore = &store.LedgerRow{
				Issue: e.Issue, Date: e.Date, Type: e.Type, WorkItemID: e.WorkItemID,
				Minutes:     e.PreviousMinutes,
				ContentHash: contentHash(Key(e.Issue, e.Date, e.Type), e.PreviousMinutes, e.Text),
				BookedAt:    now(),
			}
			result.Restored++
		default:
			result.Forgotten++
		}
		if err := repo.RevertEntry(ctx, run.ID, e, restore); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", Key(e.Issue, e.Date, e.Type), err))
		}
	}
	return result, errors.Join(errs...)
}
