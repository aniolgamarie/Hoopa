//go:build linux && amd64

package control

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"hoopa/internal/config"
)

func TestRestartWatchZeroAndClosed(t *testing.T) {
	var nilWatch *RestartWatch
	var zero RestartWatch
	for _, w := range []*RestartWatch{nilWatch, &zero} {
		if e := w.Close(); e != nil {
			t.Fatal(e)
		}
		if id, exit := w.ExitObserved(); id != "" || exit {
			t.Fatal("zero fabricated historical exit")
		}
		if _, made, e := w.Attempt(context.Background()); made || !errors.Is(e, ErrRequest) {
			t.Fatal(made, e)
		}
	}
	w := RestartWatch{&restartState{closed: true, runID: "synthetic-original", exited: true}}
	copy := w
	if _, made, e := copy.Attempt(context.Background()); made || !errors.Is(e, config.ErrClosed) {
		t.Fatal(made, e)
	}
	if id, exit := copy.ExitObserved(); id != "synthetic-original" || !exit {
		t.Fatal("lost explicit historical fixture fact")
	}
	if _, made, e := w.Attempt(nil); made || !errors.Is(e, ErrRequest) {
		t.Fatal(made, e)
	}
}
func TestRestartAuthorizationRejectsZeroWrites(t *testing.T) {
	for _, name := range []string{"nil-context", "revision", "pristine", "stopped", "pending", "unknown-owner", "closed"} {
		t.Run(name, func(t *testing.T) {
			f := lifecycleRunningFixture(t)
			ctx := context.Background()
			revision := f.base.Revision
			want := ErrRecoveryRequired
			switch name {
			case "nil-context":
				ctx = nil
				want = ErrRequest
			case "revision":
				revision = "different-revision"
				want = ErrConflict
			case "pristine":
				c, p := coordinatorFixture(t, nil)
				f.c, f.paths = c, p
				active, e := c.store.ReadActive()
				if e != nil {
					t.Fatal(e)
				}
				revision = active.Revision
			case "stopped":
				f.stopped(t)
				active, e := f.c.store.ReadActive()
				if e != nil {
					t.Fatal(e)
				}
				revision = active.Revision
			case "pending":
				f.admit(t, "pending-stop-before-watch", "stop")
			case "unknown-owner":
				want = ErrCapabilityUnsupported
			case "closed":
				if e := f.c.Close(); e != nil {
					t.Fatal(e)
				}
				want = config.ErrClosed
			}
			before := coordinatorTree(t, f.paths)
			a := recoveryApplier(t, f.c)
			w, e := a.AuthorizeRestart(ctx, revision)
			if w != nil || !errors.Is(e, want) || !reflect.DeepEqual(before, coordinatorTree(t, f.paths)) {
				t.Fatal("watch authorization mutated storage or wrong rejection", w, e, want)
			}
		})
	}
}
