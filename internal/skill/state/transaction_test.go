package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreRecoversInterruptedActivationStages(t *testing.T) {
	for _, test := range []struct {
		name            string
		moveCandidate   bool
		commitSelection bool
		wantActive      string
	}{
		{name: "previous moved", wantActive: "old"},
		{name: "candidate moved", moveCandidate: true, wantActive: "old"},
		{name: "selection committed", moveCandidate: true, commitSelection: true, wantActive: "new"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			store, previous, target, moves := prepareActivationSwap(t, root)
			transaction := store.newSwapTransaction("demo", "activate", previous, target, moves, "")
			if err := store.saveSwapTransaction(transaction); err != nil {
				t.Fatal(err)
			}
			if err := moveRecordedDirectory(store, moves[0]); err != nil {
				t.Fatal(err)
			}
			transaction.AppliedMoves = 1
			transaction.Phase = swapPhasePreviousMoved
			if err := store.saveSwapTransaction(transaction); err != nil {
				t.Fatal(err)
			}
			if test.moveCandidate {
				if err := moveRecordedDirectory(store, moves[1]); err != nil {
					t.Fatal(err)
				}
				transaction.AppliedMoves = 2
				transaction.Phase = swapPhaseCandidateMoved
				if err := store.saveSwapTransaction(transaction); err != nil {
					t.Fatal(err)
				}
			}
			if test.commitSelection {
				// Simulate termination after the atomic selection commit but before
				// the journal is advanced to state_committed.
				if err := store.persistSelection("demo", target); err != nil {
					t.Fatal(err)
				}
			}

			recovered, err := New(root)
			if err != nil {
				t.Fatal(err)
			}
			selection, err := recovered.Snapshot("demo")
			if err != nil {
				t.Fatal(err)
			}
			wantSelection := previous
			if test.commitSelection {
				wantSelection = target
			}
			if !selectionsEqual(selection, wantSelection) {
				t.Fatalf("selection = %#v, want %#v", selection, wantSelection)
			}
			activePath, err := recovered.UserPath("demo")
			if err != nil {
				t.Fatal(err)
			}
			assertSkillBody(t, activePath, test.wantActive)
			if _, err := os.Stat(recovered.swapTransactionPath("demo")); !os.IsNotExist(err) {
				t.Fatalf("swap journal remains after recovery: %v", err)
			}
		})
	}
}

func TestStoreStopsRecoveryOnSwapDigestMismatch(t *testing.T) {
	root := t.TempDir()
	store, previous, target, moves := prepareActivationSwap(t, root)
	transaction := store.newSwapTransaction("demo", "activate", previous, target, moves, "")
	if err := store.saveSwapTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	if err := moveRecordedDirectory(store, moves[0]); err != nil {
		t.Fatal(err)
	}
	transaction.AppliedMoves = 1
	transaction.Phase = swapPhasePreviousMoved
	if err := store.saveSwapTransaction(transaction); err != nil {
		t.Fatal(err)
	}
	previousArchive, err := store.resolveSwapPath(moves[0].Destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(previousArchive, "SKILL.md"), []byte("changed"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := New(root); err == nil {
		t.Fatal("New() recovered a transaction whose backup digest changed")
	}
	if _, err := os.Stat(store.swapTransactionPath("demo")); err != nil {
		t.Fatalf("swap journal was removed after unsafe recovery: %v", err)
	}
	assertSkillBody(t, previousArchive, "changed")
}

func TestStoreRecoversInterruptedInstallAndCommittedUninstall(t *testing.T) {
	t.Run("uncommitted install is discarded", func(t *testing.T) {
		root := t.TempDir()
		store, err := New(root)
		if err != nil {
			t.Fatal(err)
		}
		work, cleanupPath, err := store.transactionWorkPath("demo")
		if err != nil {
			t.Fatal(err)
		}
		candidate := filepath.Join(work, "candidate")
		writeSkillBody(t, candidate, "candidate")
		digest, _, err := directoryDigest(candidate)
		if err != nil {
			t.Fatal(err)
		}
		destination, err := store.archivePath("demo", "1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		move, err := store.newSwapMove(swapRoleCandidate, candidate, destination, digest, swapCommitKeep, swapRollbackDiscard)
		if err != nil {
			t.Fatal(err)
		}
		transaction := store.newSwapTransaction("demo", "install", Selection{}, Selection{}, []SwapMove{move}, cleanupPath)
		if err := store.saveSwapTransaction(transaction); err != nil {
			t.Fatal(err)
		}
		if err := moveRecordedDirectory(store, move); err != nil {
			t.Fatal(err)
		}
		transaction.AppliedMoves = 1
		transaction.Phase = swapPhaseCandidateMoved
		if err := store.saveSwapTransaction(transaction); err != nil {
			t.Fatal(err)
		}
		if _, err := New(root); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(destination); !os.IsNotExist(err) {
			t.Fatalf("uncommitted candidate remains: %v", err)
		}
	})

	t.Run("committed uninstall removes backup", func(t *testing.T) {
		root := t.TempDir()
		store, err := New(root)
		if err != nil {
			t.Fatal(err)
		}
		archive, err := store.archivePath("demo", "1.0.0")
		if err != nil {
			t.Fatal(err)
		}
		writeSkillBody(t, archive, "old")
		previous := Selection{History: []string{"1.0.0"}, UpdatedAt: time.Unix(100, 0).UTC()}
		target := Selection{UpdatedAt: time.Unix(101, 0).UTC()}
		if err := store.persistSelection("demo", previous); err != nil {
			t.Fatal(err)
		}
		work, cleanupPath, err := store.transactionWorkPath("demo")
		if err != nil {
			t.Fatal(err)
		}
		backup := filepath.Join(work, "version")
		digest, _, err := directoryDigest(archive)
		if err != nil {
			t.Fatal(err)
		}
		move, err := store.newSwapMove(swapRoleRemoval, archive, backup, digest, swapCommitRemove, swapRollbackRestore)
		if err != nil {
			t.Fatal(err)
		}
		transaction := store.newSwapTransaction("demo", "uninstall_version", previous, target, []SwapMove{move}, cleanupPath)
		if err := store.saveSwapTransaction(transaction); err != nil {
			t.Fatal(err)
		}
		if err := moveRecordedDirectory(store, move); err != nil {
			t.Fatal(err)
		}
		if err := store.persistSelection("demo", target); err != nil {
			t.Fatal(err)
		}
		transaction.AppliedMoves = 1
		transaction.Phase = swapPhaseStateCommitted
		if err := store.saveSwapTransaction(transaction); err != nil {
			t.Fatal(err)
		}

		recovered, err := New(root)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(archive); !os.IsNotExist(err) {
			t.Fatalf("removed version returned after recovery: %v", err)
		}
		if _, err := os.Stat(work); !os.IsNotExist(err) {
			t.Fatalf("committed removal backup remains: %v", err)
		}
		selection, err := recovered.Snapshot("demo")
		if err != nil {
			t.Fatal(err)
		}
		if !selectionsEqual(selection, target) {
			t.Fatalf("selection = %#v, want %#v", selection, target)
		}
	})
}

func prepareActivationSwap(t *testing.T, root string) (*Store, Selection, Selection, []SwapMove) {
	t.Helper()
	store, err := New(root)
	if err != nil {
		t.Fatal(err)
	}
	active, err := store.UserPath("demo")
	if err != nil {
		t.Fatal(err)
	}
	previousArchive, err := store.archivePath("demo", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	candidateArchive, err := store.archivePath("demo", "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	writeSkillBody(t, active, "old")
	writeSkillBody(t, candidateArchive, "new")
	previous := Selection{ActiveVersion: "1.0.0", UpdatedAt: time.Unix(100, 0).UTC()}
	target := Selection{ActiveVersion: "2.0.0", History: []string{"1.0.0"}, UpdatedAt: time.Unix(101, 0).UTC()}
	if err := store.persistSelection("demo", previous); err != nil {
		t.Fatal(err)
	}
	previousDigest, _, err := directoryDigest(active)
	if err != nil {
		t.Fatal(err)
	}
	candidateDigest, _, err := directoryDigest(candidateArchive)
	if err != nil {
		t.Fatal(err)
	}
	previousMove, err := store.newSwapMove(swapRolePrevious, active, previousArchive, previousDigest, swapCommitKeep, swapRollbackRestore)
	if err != nil {
		t.Fatal(err)
	}
	candidateMove, err := store.newSwapMove(swapRoleCandidate, candidateArchive, active, candidateDigest, swapCommitKeep, swapRollbackRestore)
	if err != nil {
		t.Fatal(err)
	}
	return store, previous, target, []SwapMove{previousMove, candidateMove}
}

func moveRecordedDirectory(store *Store, move SwapMove) error {
	source, err := store.resolveSwapPath(move.Source)
	if err != nil {
		return err
	}
	destination, err := store.resolveSwapPath(move.Destination)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	return os.Rename(source, destination)
}

func writeSkillBody(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "SKILL.md"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertSkillBody(t *testing.T, root, want string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("SKILL.md = %q, want %q", data, want)
	}
}
