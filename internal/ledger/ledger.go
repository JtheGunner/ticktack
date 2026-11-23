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
	"strings"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/sessionizer"
	"github.com/JtheGunner/ticktack/internal/sink"
	"github.com/JtheGunner/ticktack/internal/store"
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
	// LastBookedText returns the text last booked for an item as the sync
	// history recorded it; false when the history has none.
	LastBookedText(ctx context.Context, issue, date, typ string) (string, bool, error)
}

// Action describes what happened to one item.
type Action string

// Actions reported per item.
const (
	ActionCreated        Action = "created"
	ActionUpdated        Action = "updated"
	ActionSkipped        Action = "skipped"
	ActionRecovered      Action = "recovered"
	ActionRepaired       Action = "repaired"
	ActionDeleted        Action = "deleted"
	ActionFailed         Action = "failed"
	ActionNotSent        Action = "not-sent"
	ActionWouldCreate    Action = "dry-run-would-create"
	ActionWouldUpdate    Action = "dry-run-would-update"
	ActionWouldRecover   Action = "dry-run-would-recover"
	ActionWouldRepair    Action = "dry-run-would-repair"
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
	// RepairedReason explains why a booked item was booked again.
	RepairedReason = "booked work item was missing"
	// restoredDescription is the text of an item undo books again when the
	// text of the deleted item is unknown (entries of earlier versions).
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
	// HoldProjects lists the projects whose booked rows are left out of
	// reconciliation in this run, whatever the items and Keep say: nothing
	// reliable is known about their time, for example because their standard
	// task could not be provided (ProvisionReport.FailedProjects), so no
	// booking of theirs may be deleted on that basis.
	HoldProjects []string
	// Failed holds the results of items that failed before the run and are
	// not among the items, for example because their standard task could not
	// be provided (ProvisionReport.Failed). They are reported as they are;
	// nothing is sent for them.
	Failed []ItemResult
	// Describer builds the text of an item, for example from commit subjects.
	// Nil keeps the description of the item.
	Describer Describer
}

// Describer builds the description of a day item at booking time. It may read
// from outside (a repository), which the pure sessionizer must not; it must
// fall back to it.Description when it has nothing better.
type Describer interface {
	Describe(ctx context.Context, it sessionizer.DayItem) string
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
	// Repaired counts booked items that were missing in the sink and were
	// booked again.
	Repaired int
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
	case ActionRepaired:
		r.Repaired++
	case ActionDeleted:
		r.Deleted++
	case ActionSkipped:
		r.Skipped++
	case ActionFailed:
		r.Failed++
	case ActionNotSent:
		r.NotSent++
	case ActionWouldCreate, ActionWouldUpdate, ActionWouldRecover, ActionWouldRepair, ActionWouldDelete:
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
// Other failures are recorded per item and the run continues. The results in
// opts.Failed are reported first.
//
// A booked item that is missing in the sink is repaired: an item carrying the
// marker is adopted, otherwise the item is created again, and the ledger row
// is replaced. Finding out costs one existence lookup by id per unchanged
// item; the marker lookup follows only for an item that is known to be
// missing.
//
// After the items, the days in opts.Reconcile are reconciled: booked rows the
// items no longer cover are deleted, except those of opts.HoldProjects. A run
// that stopped early reconciles nothing, because its items were not all
// processed.
func Sync(ctx context.Context, items []sessionizer.DayItem, s sink.TimeSink, opts SyncOptions) (Report, error) {
	if opts.Repo == nil {
		return Report{}, errors.New("ledger: no repository configured")
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	sorted := sortedItems(items)

	run := &syncRun{sink: s, opts: opts}
	var report Report
	for _, res := range opts.Failed {
		report.add(res)
	}
	for i, it := range sorted {
		if err := ctx.Err(); err != nil {
			markNotSent(&report, sorted[i:])
			return report, err
		}
		itemCtx, done := inFlight(ctx)
		res, err := run.syncItem(itemCtx, it)
		done()
		report.add(res)
		if IsStopError(err) {
			markNotSent(&report, sorted[i+1:])
			return report, err
		}
	}
	if err := run.reconcile(ctx, sorted, &report); err != nil {
		return report, err
	}
	return report, nil
}

// inFlightTimeout bounds the work on one item, with or without a signal. It is
// a variable only so that tests can lower it. The YouTrack client has its own
// per-request timeout (30 seconds), which normally ends a hung call first; this
// bound is the backstop for a sink that does not honour one, and it is larger
// than a request so that a slow but healthy call plus its retry lookup and the
// ledger write fit in.
var inFlightTimeout = 2 * time.Minute

// inFlight returns the context of one item's work: the sink call and the
// ledger write that follows it. It is detached from ctx on purpose. A signal
// cancels ctx, and an item whose sink call went through but whose ledger write
// failed on the cancelled context would exist in YouTrack without a ledger row.
// The loops check ctx between items instead, so a cancellation stops the run
// before the next item starts, never inside one. The timeout keeps an item
// that hangs on the network from holding the process forever; a second signal
// ends it at once (the entrypoint restores the default handling after the
// first one).
func inFlight(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), inFlightTimeout)
}

// sortedItems returns a copy of items in date, issue, type order.
func sortedItems(items []sessionizer.DayItem) []sessionizer.DayItem {
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
	return sorted
}

// NotSentReport is the report of a run that stopped before any item was sent,
// for example because providing the standard tasks hit a stop error: every
// item is reported as not sent, in the order Sync would have processed them.
func NotSentReport(items []sessionizer.DayItem) Report {
	var report Report
	markNotSent(&report, sortedItems(items))
	return report
}

// reconcile deletes the booked rows of the reconciled days that no item and
// no kept key covers and that do not belong to a held project. A failed deletion is reported and the others continue;
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
			if covered[Key(row.Issue, row.Date, row.Type)] || inAnyProject(row.Issue, r.opts.HoldProjects) {
				continue
			}
			stale = append(stale, row)
		}
	}
	for i, row := range stale {
		if err := ctx.Err(); err != nil {
			markRowsNotSent(report, stale[i:])
			return err
		}
		rowCtx, done := inFlight(ctx)
		res, err := r.deleteRow(rowCtx, row)
		done()
		report.add(res)
		if IsStopError(err) {
			markRowsNotSent(report, stale[i+1:])
			return err
		}
	}
	return nil
}

// inAnyProject reports whether issue belongs to one of projects. An issue key
// is its project key, a dash and a number; the comparison ignores case, as
// the sink does when it searches a project, so a held project is never missed
// over the spelling of its key.
func inAnyProject(issue string, projects []string) bool {
	i := strings.LastIndex(issue, "-")
	if i <= 0 {
		return false
	}
	for _, project := range projects {
		if strings.EqualFold(issue[:i], project) {
			return true
		}
	}
	return false
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
		if IsStopError(err) {
			return res, err
		}
		return res, nil
	}
	if err := r.sink.Delete(ctx, row.WorkItemID); err != nil && !errors.Is(err, sink.ErrNotFound) {
		return fail(err)
	}
	// The text the item had is kept with the entry, so undo books it again as
	// it was. The sink call has gone through, so a failed read only costs the
	// text: undo then falls back to the generic one.
	previousText, _, _ := r.opts.Repo.LastBookedText(ctx, row.Issue, row.Date, row.Type)
	runID, err := r.ensureRun(ctx, r.opts.Now())
	if err != nil {
		return fail(err)
	}
	entry := store.SyncEntry{
		Issue: row.Issue, Date: row.Date, Type: row.Type, Action: entryActionDeleted, WorkItemID: row.WorkItemID,
		PreviousMinutes: row.Minutes, Text: restoredDescription + " " + Marker(key), PreviousText: previousText,
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

// IsStopError reports whether err ends a run instead of failing one item: an
// unauthorized or rate-limited sink would fail every following call as well.
func IsStopError(err error) bool {
	return errors.Is(err, sink.ErrUnauthorized) || errors.Is(err, sink.ErrRateLimited)
}

// isClientError reports errors after which Create certainly stored nothing or
// retrying the lookup is pointless (rate limits stop the run).
func isClientError(err error) bool {
	for _, target := range []error{
		sink.ErrUnauthorized, sink.ErrForbidden, sink.ErrNotFound,
		sink.ErrTimeTrackingDisabled, sink.ErrWorkItemTypeNotFound, sink.ErrRateLimited,
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

// description returns the text of an item without its marker: the describer's
// text when one is configured, the item's own description otherwise. The
// marker is appended by the caller, always at the end.
func (r *syncRun) description(ctx context.Context, it sessionizer.DayItem) string {
	if r.opts.Describer == nil {
		return it.Description
	}
	return r.opts.Describer.Describe(ctx, it)
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
		if IsStopError(err) {
			return res, err
		}
		return res, nil
	}

	work := sink.WorkItem{
		Issue: it.Ticket, Date: it.Date, Minutes: it.Minutes, Type: typ,
		Text: r.description(ctx, it) + " " + Marker(key),
	}
	row, inLedger, err := r.opts.Repo.LedgerGet(ctx, it.Ticket, date, typ)
	if err != nil {
		return failItem(err)
	}

	if inLedger {
		// The hash covers minutes and text: an item is left alone only when
		// neither changed since it was booked.
		if row.Minutes == it.Minutes && row.ContentHash == contentHash(key, work.Minutes, work.Text) {
			return r.verifyBooked(ctx, work, row, res)
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
		// A create is never repeated, so its failure leaves the outcome
		// unclear: the server may have stored the item despite the error.
		// Look it up before giving up.
		recovered, ok, findErr := r.sink.FindByMarker(ctx, it.Ticket, Marker(key))
		if findErr != nil || !ok {
			return failItem(err)
		}
		id = recovered
		// Every item found by marker is recorded as recovered, never as
		// created: undo must not delete an item that may predate this run.
		action, entryAction = ActionRecovered, entryActionRecovered
	}
	if err := r.record(ctx, work, key, id, entryAction, 0, ""); err != nil {
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
	// The text being replaced is kept with the entry so undo can restore it.
	// Only an update of a booked row has one; an adopted item's text is unknown.
	var previousText string
	if entryAction == entryActionUpdated {
		text, _, err := r.opts.Repo.LastBookedText(ctx, row.Issue, row.Date, row.Type)
		if err != nil {
			return failed(res, err)
		}
		previousText = text
	}
	if err := r.sink.Update(ctx, row.WorkItemID, work); err != nil {
		// Only a row of the ledger can point to an item that is gone; an item
		// found by marker a moment ago is not looked up a second time.
		if entryAction == entryActionUpdated && errors.Is(err, sink.ErrNotFound) {
			return r.replaceMissing(ctx, work, row, res, err)
		}
		return failed(res, err)
	}
	if err := r.record(ctx, work, res.Key, row.WorkItemID, entryAction, previous, previousText); err != nil {
		res.Action, res.Error = ActionFailed, err.Error()
		return res, nil
	}
	res.Action = done
	return res, nil
}

// failed marks res as failed. Only a stop error is returned.
func failed(res ItemResult, err error) (ItemResult, error) {
	res.Action, res.Error = ActionFailed, err.Error()
	if IsStopError(err) {
		return res, err
	}
	return res, nil
}

// verifyBooked checks that the work item of an unchanged row still exists and
// repairs the row when it does not. Without the check a row whose item was
// deleted in the sink would be reported as unchanged forever although nothing
// is booked.
//
// Existence is established by the item's id, one lookup per row, never by the
// marker lookup: a marker lookup that finds nothing, or finds another item,
// does not show that the booked item is gone, and repairing on that basis
// would book the time twice. The marker lookup runs only once the item is
// known to be missing; a failed existence check leaves the row untouched.
func (r *syncRun) verifyBooked(ctx context.Context, work sink.WorkItem, row store.LedgerRow, res ItemResult) (ItemResult, error) {
	exists, err := r.sink.WorkItemExists(ctx, work.Issue, row.WorkItemID)
	if err != nil {
		return failed(res, err)
	}
	if exists {
		res.Action, res.Reason = ActionSkipped, skipReasonUnchanged
		return res, nil
	}
	id, found, err := r.sink.FindByMarker(ctx, work.Issue, Marker(res.Key))
	if err != nil {
		return failed(res, err)
	}
	return r.repair(ctx, work, res, id, found)
}

// replaceMissing repairs a row whose update reported the work item as not
// found, which already shows that the row's own item is gone. The sink
// reports a missing issue the same way, so the marker lookup decides: when it
// fails, or still lists the item, the update failure stands.
func (r *syncRun) replaceMissing(ctx context.Context, work sink.WorkItem, row store.LedgerRow, res ItemResult, updateErr error) (ItemResult, error) {
	id, found, err := r.sink.FindByMarker(ctx, work.Issue, Marker(res.Key))
	if err != nil {
		return failed(res, err)
	}
	if found && id == row.WorkItemID {
		return failed(res, updateErr)
	}
	return r.repair(ctx, work, res, id, found)
}

// repair books an item again whose ledger row points to a work item that is
// gone. An item that carries the marker (id, found) is adopted and rewritten,
// otherwise a new one is created; either way the ledger row is replaced. An
// adopted item is recorded as recovered, so undo never deletes an item that
// may predate this run. A failed create is not looked up here: the row stays
// as it is and the next sync adopts the item if the create did land.
//
// Callers must have established that the row's own item is missing before
// they call repair. The marker lookup behind (id, found) is reads all
// pages of the issue's work items, so an item carrying the marker is found
// wherever it sits; a failed lookup is an error, not a missing item.
func (r *syncRun) repair(ctx context.Context, work sink.WorkItem, res ItemResult, id string, found bool) (ItemResult, error) {
	res.Reason = RepairedReason
	if r.opts.DryRun {
		res.Action = ActionWouldRepair
		return res, nil
	}
	entryAction := entryActionRecovered
	var err error
	if found {
		err = r.sink.Update(ctx, id, work)
	} else {
		entryAction = entryActionCreated
		id, err = r.sink.Create(ctx, work)
	}
	if err == nil {
		err = r.record(ctx, work, res.Key, id, entryAction, 0, "")
	}
	if err != nil {
		res.Reason = ""
		return failed(res, err)
	}
	res.Action = ActionRepaired
	return res, nil
}

// record writes the ledger row and the run entry after a successful sink call.
func (r *syncRun) record(ctx context.Context, w sink.WorkItem, key, id, entryAction string, previous int, previousText string) error {
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
		PreviousMinutes: previous, NewMinutes: w.Minutes, Text: w.Text, PreviousText: previousText,
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
// unknown) and are only dropped from the ledger, and so is an updated item
// that no longer exists in the sink: there is nothing left to restore. An
// entry is cleared only after its sink call succeeded, so a failed undo can be
// retried.
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
		// A cancellation stops the undo between entries, never inside one: the
		// untouched entries stay in the run, so the next undo reverts them.
		if err := ctx.Err(); err != nil {
			return result, errors.Join(append(errs, err)...)
		}
		entryCtx, done := inFlight(ctx)
		stop, entryErr := undoEntry(entryCtx, s, repo, run.ID, e, now, &result)
		done()
		if entryErr != nil {
			errs = append(errs, entryErr)
		}
		if stop {
			return result, errors.Join(errs...)
		}
	}
	return result, errors.Join(errs...)
}

// undoEntry reverts one entry of a run: the sink call and the ledger write that
// clears the entry. It reports the failure, if any, and whether the run ends
// there (a stop error).
func undoEntry(ctx context.Context, s sink.TimeSink, repo Repository, runID int64, e store.SyncEntry,
	now func() time.Time, result *UndoResult) (stop bool, err error) {
	key := Key(e.Issue, e.Date, e.Type)
	date, parseErr := civil.ParseDate(e.Date)
	if parseErr != nil {
		return false, fmt.Errorf("%s: invalid date %q", key, e.Date)
	}
	fail := func(err error) (bool, error) {
		return IsStopError(err), fmt.Errorf("%s: %w", key, err)
	}
	var restore *store.LedgerRow
	switch e.Action {
	case entryActionCreated:
		if err := s.Delete(ctx, e.WorkItemID); err != nil && !errors.Is(err, sink.ErrNotFound) {
			return fail(err)
		}
		result.Deleted++
	case entryActionUpdated:
		// An entry of an earlier version has no previous text; the minutes
		// are restored with the text that was sent then.
		text := e.PreviousText
		if text == "" {
			text = e.Text
		}
		w := sink.WorkItem{Issue: e.Issue, Date: date, Minutes: e.PreviousMinutes, Type: e.Type, Text: text}
		err := s.Update(ctx, e.WorkItemID, w)
		switch {
		case errors.Is(err, sink.ErrNotFound):
			// The item is gone, so the entry is done: drop the row, or
			// this run would stay the last one forever.
			result.Forgotten++
		case err != nil:
			return fail(err)
		default:
			restore = &store.LedgerRow{
				Issue: e.Issue, Date: e.Date, Type: e.Type, WorkItemID: e.WorkItemID,
				Minutes:     e.PreviousMinutes,
				ContentHash: contentHash(key, e.PreviousMinutes, text),
				BookedAt:    now(),
			}
			result.Restored++
		}
	case entryActionDeleted:
		text := e.PreviousText
		if text == "" {
			text = e.Text
		}
		w := sink.WorkItem{Issue: e.Issue, Date: date, Minutes: e.PreviousMinutes, Type: e.Type, Text: text}
		id, err := recreate(ctx, s, w, Marker(key))
		if err != nil {
			return fail(err)
		}
		restore = &store.LedgerRow{
			Issue: e.Issue, Date: e.Date, Type: e.Type, WorkItemID: id,
			Minutes:     e.PreviousMinutes,
			ContentHash: contentHash(key, e.PreviousMinutes, text),
			BookedAt:    now(),
		}
		result.Recreated++
	default:
		result.Forgotten++
	}
	if err := repo.RevertEntry(ctx, runID, e, restore); err != nil {
		return false, fmt.Errorf("%s: %w", key, err)
	}
	return false, nil
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
