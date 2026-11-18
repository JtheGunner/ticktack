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
	LedgerRowsForDate(ctx context.Context, date string) ([]store.LedgerRow, error)
	BeginSyncRun(ctx context.Context, startedAt time.Time) (int64, error)
	RecordBooking(ctx context.Context, runID int64, row store.LedgerRow, e store.SyncEntry) error
	RecordDeletion(ctx context.Context, runID int64, row store.LedgerRow, e store.SyncEntry) error
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
	ActionDeleted        Action = "deleted"
	ActionFailed         Action = "failed"
	ActionNotSent        Action = "not-sent"
	ActionWouldCreate    Action = "dry-run-would-create"
	ActionWouldUpdate    Action = "dry-run-would-update"
	ActionWouldRecover   Action = "dry-run-would-recover"
	ActionWouldDelete    Action = "dry-run-would-delete"
	skipReasonNoTicket          = "no ticket"
	skipReasonUnchanged         = "unchanged"
	entryActionCreated          = "created"
	entryActionUpdated          = "updated"
	entryActionRecovered        = "recovered"
	entryActionDeleted          = "deleted"
	notSentReason               = "not sent: run stopped"
	markerPrefix                = "[ticktack:"
	// DeletedReason explains why a booked item was removed.
	DeletedReason = "no longer part of the day"
	// restoredDescription is the text of an item undo books again: the
	// original description of a deleted item is not kept.
	restoredDescription = "restored by ticktack undo"
)

// SyncOptions configures Sync.
type SyncOptions struct {
	// Repo is the ledger storage.
	Repo Repository
	// DryRun performs read calls only: no ledger writes and no mutating sink call.
	DryRun bool
	// Now returns the current time; it defaults to time.Now.
	Now func() time.Time
	// Reconcile lists the days whose booked state must match the items: a
	// ledger row of such a day whose key is neither among the items nor in
	// Keep is deleted from the sink and the ledger. Other days are untouched.
	Reconcile []civil.Date
	// Keep holds the keys of rows that stay booked without an item, for
	// example items the day still has but that may not be booked right now.
	Keep []string
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
	// Deleted counts booked items that were removed by reconciliation.
	Deleted int
	Skipped int
	Failed  int
	NotSent int
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
	case ActionDeleted:
		r.Deleted++
	case ActionSkipped:
		r.Skipped++
	case ActionFailed:
		r.Failed++
	case ActionNotSent:
		r.NotSent++
	case ActionWouldCreate, ActionWouldUpdate, ActionWouldRecover, ActionWouldDelete:
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
//
// After the items, the days in opts.Reconcile are reconciled: booked rows the
// items no longer cover are deleted. A run that stopped early reconciles
// nothing, because its items were not all processed.
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
	if err := run.reconcile(ctx, sorted, &report); err != nil {
		return report, err
	}
	return report, nil
}

// reconcile deletes the booked rows of the reconciled days that no item and
// no kept key covers. A failed deletion is reported and the others continue;
// a stop error ends the run with the remaining rows reported as not sent.
func (r *syncRun) reconcile(ctx context.Context, items []sessionizer.DayItem, report *Report) error {
	if len(r.opts.Reconcile) == 0 {
		return nil
	}
	covered := make(map[string]bool, len(items)+len(r.opts.Keep))
	for _, it := range items {
		if it.Ticket != "" {
			covered[itemKey(it)] = true
		}
	}
	for _, key := range r.opts.Keep {
		covered[key] = true
	}
	var stale []store.LedgerRow
	for _, date := range uniqueDates(r.opts.Reconcile) {
		rows, err := r.opts.Repo.LedgerRowsForDate(ctx, date.String())
		if err != nil {
			return err
		}
		for _, row := range rows {
			if !covered[Key(row.Issue, row.Date, row.Type)] {
				stale = append(stale, row)
			}
		}
	}
	for i, row := range stale {
		if err := ctx.Err(); err != nil {
			markRowsNotSent(report, stale[i:])
			return err
		}
		res, err := r.deleteRow(ctx, row)
		report.add(res)
		if isStopError(err) {
			markRowsNotSent(report, stale[i+1:])
			return err
		}
	}
	return nil
}

func uniqueDates(dates []civil.Date) []civil.Date {
	out := append([]civil.Date(nil), dates...)
	sort.Slice(out, func(i, j int) bool { return out[i].Before(out[j]) })
	kept := out[:0]
	for i, d := range out {
		if i == 0 || d != out[i-1] {
			kept = append(kept, d)
		}
	}
	return kept
}

func markRowsNotSent(r *Report, rest []store.LedgerRow) {
	for _, row := range rest {
		r.add(ItemResult{Key: Key(row.Issue, row.Date, row.Type), Action: ActionNotSent, Minutes: row.Minutes, Reason: notSentReason})
	}
}

// deleteRow removes one stale row. Only a stop error is returned; any other
// failure is carried by the result. A work item that is already gone counts
// as deleted.
func (r *syncRun) deleteRow(ctx context.Context, row store.LedgerRow) (ItemResult, error) {
	key := Key(row.Issue, row.Date, row.Type)
	res := ItemResult{Key: key, Minutes: row.Minutes, Reason: DeletedReason}
	if r.opts.DryRun {
		res.Action = ActionWouldDelete
		return res, nil
	}
	fail := func(err error) (ItemResult, error) {
		res.Action, res.Reason, res.Error = ActionFailed, "", err.Error()
		if isStopError(err) {
			return res, err
		}
		return res, nil
	}
	if err := r.sink.Delete(ctx, row.WorkItemID); err != nil && !errors.Is(err, youtrack.ErrIssueNotFound) {
		return fail(err)
	}
	runID, err := r.ensureRun(ctx, r.opts.Now())
	if err != nil {
		return fail(err)
	}
	entry := store.SyncEntry{
		Issue: row.Issue, Date: row.Date, Type: row.Type, Action: entryActionDeleted, WorkItemID: row.WorkItemID,
		PreviousMinutes: row.Minutes, Text: restoredDescription + " " + Marker(key),
	}
	if err := r.opts.Repo.RecordDeletion(ctx, runID, row, entry); err != nil {
		return fail(err)
	}
	res.Action = ActionDeleted
	return res, nil
}

// ensureRun starts the sync run on the first change and returns its id.
func (r *syncRun) ensureRun(ctx context.Context, now time.Time) (int64, error) {
	if r.runID == 0 {
		runID, err := r.opts.Repo.BeginSyncRun(ctx, now)
		if err != nil {
			return 0, err
		}
		r.runID = runID
	}
	return r.runID, nil
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
	runID, err := r.ensureRun(ctx, now)
	if err != nil {
		return err
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
	return r.opts.Repo.RecordBooking(ctx, runID, row, entry)
}

func contentHash(key string, minutes int, text string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%s", key, minutes, text)))
	return hex.EncodeToString(sum[:])
}

// UndoResult reports what Undo did.
type UndoResult struct {
	// Found is false when there was no sync run to undo.
	Found    bool
	Deleted  int
	Restored int
	// Recreated counts items the run had deleted that were booked again.
	Recreated int
	Forgotten int
}

// Undo reverts the last sync run only: created items are deleted from the
// sink and removed from the ledger, updated items are restored to their
// previous minutes, and items the run deleted are booked again with their
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
		case entryActionDeleted:
			w := sink.WorkItem{Issue: e.Issue, Date: date, Minutes: e.PreviousMinutes, Type: e.Type, Text: e.Text}
			id, err := recreate(ctx, s, w, Marker(Key(e.Issue, e.Date, e.Type)))
			if err != nil {
				errs = append(errs, fmt.Errorf("%s: %w", Key(e.Issue, e.Date, e.Type), err))
				if isStopError(err) {
					return result, errors.Join(errs...)
				}
				continue
			}
			restore = &store.LedgerRow{
				Issue: e.Issue, Date: e.Date, Type: e.Type, WorkItemID: id,
				Minutes:     e.PreviousMinutes,
				ContentHash: contentHash(Key(e.Issue, e.Date, e.Type), e.PreviousMinutes, e.Text),
				BookedAt:    now(),
			}
			result.Recreated++
		default:
			result.Forgotten++
		}
		if err := repo.RevertEntry(ctx, run.ID, e, restore); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", Key(e.Issue, e.Date, e.Type), err))
		}
	}
	return result, errors.Join(errs...)
}

// recreate books w again and returns its id. An item that already carries the
// marker, left by an undo that failed halfway, is reused instead of
// duplicated.
func recreate(ctx context.Context, s sink.TimeSink, w sink.WorkItem, marker string) (string, error) {
	id, found, err := s.FindByMarker(ctx, w.Issue, marker)
	if err != nil {
		return "", err
	}
	if !found {
		return s.Create(ctx, w)
	}
	if err := s.Update(ctx, id, w); err != nil {
		return "", err
	}
	return id, nil
}
