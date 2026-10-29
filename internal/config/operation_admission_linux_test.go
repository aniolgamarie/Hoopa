//go:build linux

package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOperationAdmissionPreflightIsReadOnly(t *testing.T) {
	for _, scenario := range []string{"empty", "readonly", "staging", "unknown", "capacity", "poisoned", "closed"} {
		t.Run(scenario, func(t *testing.T) {
			p := storePaths(t)
			w := openWriter(t, p)
			var want error
			var read *Store
			s := w
			switch scenario {
			case "readonly":
				var err error
				read, err = OpenStore(p, false)
				if err != nil {
					t.Fatal(err)
				}
				defer read.Close()
				s, want = read, ErrReadOnly
			case "staging":
				if err := os.WriteFile(filepath.Join(p.State, "operations", ".operation-"+strings.Repeat("a", 48)), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				want = ErrOperationStaging
			case "unknown":
				if err := os.WriteFile(filepath.Join(p.State, "operations", "unknown"), []byte(`{}`), 0600); err != nil {
					t.Fatal(err)
				}
				want = ErrIntegrity
			case "capacity":
				// Actual opaque storage names: semantic journal decoding is separate.
				for n := 0; n < MaxOperationFiles; n++ {
					name, _ := operationName(operationKey(fmt.Sprint(n)))
					if err := os.WriteFile(filepath.Join(p.State, "operations", name), []byte(`{}`), 0600); err != nil {
						t.Fatal(err)
					}
				}
				want = ErrOperationCapacity
			case "poisoned":
				w.fault = func(point string) error {
					if point == "operation.parent-sync" {
						return errors.New("injected sync failure")
					}
					return nil
				}
				if _, err := w.CompareAndSwapOperation(operationKey("poison"), "", []byte(`{}`)); !errors.Is(err, ErrUncertain) {
					t.Fatal(err)
				}
				w.fault = nil
				want = ErrUncertain
			case "closed":
				if err := w.Close(); err != nil {
					t.Fatal(err)
				}
				want = ErrClosed
			}
			before := tree(t, p.State)
			if err := s.CheckOperationAdmission(); !errors.Is(err, want) {
				t.Fatal(err, want)
			}
			if !reflect.DeepEqual(before, tree(t, p.State)) {
				t.Fatal("preflight changed authority")
			}
		})
	}
}
