//go:build linux && amd64

package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"hoopa/internal/config"
	"hoopa/internal/platform"
)

func TestLauncherClaimClosesInheritedDescriptor(t *testing.T) {
	for _, name := range []string{"fresh", "wrong-nonce", "wrong-intent", "cancelled", "repeat-birth", "nil-context"} {
		t.Run(name, func(t *testing.T) {
			f, ticket := ownershipPrepared(t, "127.0.0.1:23456", false)
			t.Setenv("HOOPA_RUN_NONCE", f.nonce)
			inherited, err := ticket.TakeBirthDirectory()
			if err != nil {
				t.Fatal(err)
			}
			defer inherited.Close()
			nonce, sha := f.nonce, ticket.Digest()
			ctx := context.Background()
			switch name {
			case "wrong-nonce":
				nonce = strings.Repeat("cd", 32)
				t.Setenv("HOOPA_RUN_NONCE", nonce)
			case "wrong-intent":
				sha = ownershipDigest([]byte("other-intent"))
			case "cancelled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			case "nil-context":
				ctx = nil
			case "repeat-birth":
				if _, err := ClaimLauncher(ctx, inherited, sha, nonce); err != nil {
					t.Fatal(err)
				}
				inherited, err = os.Open(f.runPath)
				if err != nil {
					t.Fatal(err)
				}
				defer inherited.Close()
			}
			before := ownershipTree(t, f.paths.State)
			intent, err := ClaimLauncher(ctx, inherited, sha, nonce)
			if _, closedErr := inherited.Stat(); !errors.Is(closedErr, os.ErrClosed) {
				t.Fatal("inherited descriptor not consumed:", closedErr)
			}
			if name == "fresh" {
				if err != nil || !reflect.DeepEqual(intent, f.intent) {
					t.Fatal("fresh launcher claim:", err)
				}
				birth, _, err := f.store.ReadRunBirth(f.intent.RunID)
				if err != nil {
					t.Fatal(err)
				}
				actual, err := platform.CurrentProcessBirth(ownershipContext(t))
				if err != nil {
					t.Fatal(err)
				}
				if birth.PID != actual.PID || birth.UID != actual.UID || birth.BootID != actual.BootID || birth.StartTicks != actual.StartTicks {
					t.Fatal("claim did not preserve actual launcher birth")
				}
			} else {
				if err == nil {
					t.Fatal("invalid claim accepted")
				}
				if name == "cancelled" && !errors.Is(err, context.Canceled) {
					t.Fatal("context error lost:", err)
				}
				if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
					t.Fatal("failed launcher changed records")
				}
				if name != "repeat-birth" {
					if _, err := os.Stat(filepath.Join(f.runPath, "birth.json")); !errors.Is(err, os.ErrNotExist) {
						t.Fatal("failed claim published birth:", err)
					}
				}
			}
		})
	}
	if _, err := ClaimLauncher(context.Background(), nil, "", ""); !errors.Is(err, ErrOwnershipUnknown) {
		t.Fatal("nil directory accepted")
	}
}

func TestLauncherUnfinishedBirthCannotAuthorizeExec(t *testing.T) {
	f, ticket := ownershipPrepared(t, "127.0.0.1:23456", false)
	t.Setenv("HOOPA_RUN_NONCE", f.nonce)
	inherited, err := ticket.TakeBirthDirectory()
	if err != nil {
		t.Fatal(err)
	}
	// An existing unfinished birth slot is a real filesystem publication conflict;
	// no package-private filesystem fault injection or process execution is used.
	if err := os.WriteFile(filepath.Join(f.runPath, "birth.json"), []byte("{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	before := ownershipTree(t, f.paths.State)
	if _, err := ClaimLauncher(context.Background(), inherited, ticket.Digest(), f.nonce); !errors.Is(err, config.ErrRunConflict) {
		t.Fatal("unfinished birth allowed claim:", err)
	}
	if _, err := inherited.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("failed birth left inherited descriptor open")
	}
	if !reflect.DeepEqual(before, ownershipTree(t, f.paths.State)) {
		t.Fatal("failed birth repaired original")
	}
}
