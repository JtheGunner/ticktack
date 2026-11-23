package cli

import (
	"context"
	"errors"
	"time"

	"cloud.google.com/go/civil"

	"github.com/JtheGunner/ticktack/internal/store"
)

// archivedDayReader is the part of the store that tells whether a day was
// cleaned by the retention cleanup.
type archivedDayReader interface {
	ArchivedDay(ctx context.Context, day string) (store.ArchivedDay, bool, error)
}

// refuseArchivedDay returns a clear error when day was archived by the
// retention cleanup. Such a day has no events left, so deriving it would show
// an empty day, and a sync of it would look like a day without time. The
// commands that derive a day call this before they do.
func refuseArchivedDay(ctx context.Context, st archivedDayReader, day civil.Date) error {
	archived, found, err := st.ArchivedDay(ctx, day.String())
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	return errors.New(msgDayArchived(day.String(), archived.EventsDeleted, archived.ArchivedAt.UTC().Format(dayLayout)))
}

// openStoreForDay opens the store with open and refuses day when it was
// archived. The caller closes the returned store; on an error it is already
// closed.
func openStoreForDay[S interface {
	archivedDayReader
	Close() error
}](ctx context.Context, open func() (S, error), day civil.Date) (S, error) {
	st, err := open()
	if err != nil {
		var zero S
		return zero, err
	}
	if err := refuseArchivedDay(ctx, st, day); err != nil {
		_ = st.Close()
		var zero S
		return zero, err
	}
	return st, nil
}

// civilDayOf returns the local calendar day of t in loc.
func civilDayOf(t time.Time, loc *time.Location) civil.Date {
	if loc == nil {
		loc = time.Local
	}
	return civil.DateOf(t.In(loc))
}
