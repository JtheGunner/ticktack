package sink

import "errors"

// Typed errors a TimeSink or TaskProvisioner returns (usually wrapped), for
// use with errors.Is. They live here so callers such as the ledger can
// classify failures without importing a concrete sink. The message texts name
// YouTrack because it is the only sink today and users read these messages.
var (
	// ErrUnauthorized: the token is invalid or expired.
	ErrUnauthorized = errors.New("youtrack: unauthorized (token invalid or expired)")
	// ErrForbidden: time tracking is disabled or the user lacks permission.
	ErrForbidden = errors.New("youtrack: forbidden (time tracking disabled or no permission)")
	// ErrNotFound: the target answered "not found" (HTTP 404). Despite the
	// message it is returned for every 404, so the missing object can be the
	// issue, the work item or the endpoint; callers treat it as "gone".
	ErrNotFound = errors.New("youtrack: issue not found")
	// ErrRateLimited: the target asked the caller to slow down.
	ErrRateLimited = errors.New("youtrack: rate limited")
	// ErrTimeTrackingDisabled: time tracking is off for the project.
	ErrTimeTrackingDisabled = errors.New("youtrack: time tracking is disabled for the project")
	// ErrWorkItemTypeNotFound: the work item type does not exist in the project.
	ErrWorkItemTypeNotFound = errors.New("youtrack: work item type not found in project")
	// ErrOutcomeUnclear marks a failed create request that the server may
	// have stored all the same: no usable answer arrived. Such a request is
	// never repeated; the caller looks the object up by its marker instead.
	ErrOutcomeUnclear = errors.New("youtrack: outcome unclear (the request may have been stored)")
)
