package vault

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"
)

func exportFixtureTokens(t *testing.T, store *Store) []Token {
	t.Helper()
	var tokens []Token
	for i, secret := range []string{"JBSWY3DPEHPK3PXP", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "KRSXG5A"} {
		input := testInput([]string{"first@example.test", "second@example.test", "third@example.test"}[i])
		input.Secret = secret
		added, err := store.Add(input)
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, added)
	}
	return tokens
}

func TestExportSelectionScopeRoundTripAndInvalidIDs(t *testing.T) {
	store, reopen := newTestStore(t)
	tokens := exportFixtureTokens(t, store)
	before := readFile(t, store.path)
	snapshot, err := store.ExportSelection([]string{tokens[2].ID, tokens[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	credentials := snapshot.Credentials()
	if len(credentials) != 2 || credentials[0].ID != tokens[2].ID || credentials[1].ID != tokens[0].ID || strings.Contains(snapshot.Text(), "second%40") || strings.Contains(snapshot.Text(), "second@example.test") {
		t.Fatal("export selection included unselected or reordered accounts")
	}
	for _, credential := range credentials {
		input, err := PreviewURI(credential.URI)
		if err != nil || input.Account != credential.Account || input.Secret != credential.Secret {
			t.Fatal("selected credential did not round-trip")
		}
	}
	credentials[0].Secret = "tampered"
	if snapshot.Credentials()[0].Secret == "tampered" {
		t.Fatal("caller mutated private snapshot")
	}
	for _, ids := range [][]string{nil, {}, {tokens[0].ID, tokens[0].ID}, {tokens[0].ID, "missing"}, make([]string, 101)} {
		if got, err := store.ExportSelection(ids); err == nil || got != nil {
			t.Fatal("invalid export selection was accepted")
		}
	}
	if !bytes.Equal(before, readFile(t, store.path)) {
		t.Fatal("preview changed encrypted storage")
	}
	if got, _ := reopen().List(); !reflect.DeepEqual(got, tokens) {
		t.Fatal("preview changed persisted accounts")
	}
}

func TestDeleteExportSnapshotOnlyDeletesExactUnchangedSelection(t *testing.T) {
	store, reopen := newTestStore(t)
	tokens := exportFixtureTokens(t, store)
	snapshot, err := store.ExportSelection([]string{tokens[2].ID, tokens[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteExportSnapshot(snapshot); err != nil {
		t.Fatal(err)
	}
	if got, _ := reopen().List(); !reflect.DeepEqual(got, tokens[1:2]) {
		t.Fatal("deletion touched an unselected account")
	}
	if err := store.DeleteExportSnapshot(snapshot); err == nil {
		t.Fatal("already deleted snapshot was accepted")
	}
}

func TestDeleteExportSnapshotRejectsStaleSelectionAndSaveFailureAtomically(t *testing.T) {
	for _, mode := range []string{"changed", "missing", "save failure"} {
		t.Run(mode, func(t *testing.T) {
			store, reopen := newTestStore(t)
			tokens := exportFixtureTokens(t, store)
			snapshot, _ := store.ExportSelection([]string{tokens[0].ID, tokens[1].ID})
			switch mode {
			case "changed":
				input := testInput("changed@example.test")
				input.Secret = ""
				if _, err := store.Update(tokens[1].ID, input); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if err := store.Delete(tokens[1].ID); err != nil {
					t.Fatal(err)
				}
			case "save failure":
				store.write = func(string, []byte) error { return errors.New("synthetic failure") }
			}
			before := readFile(t, store.path)
			prior, _ := store.List()
			if err := store.DeleteExportSnapshot(snapshot); err == nil {
				t.Fatal("stale or unsavable deletion succeeded")
			}
			if !bytes.Equal(before, readFile(t, store.path)) {
				t.Fatal("failed deletion replaced vault")
			}
			if got, _ := store.List(); !reflect.DeepEqual(got, prior) {
				t.Fatal("failed deletion changed memory")
			}
			if got, _ := reopen().List(); !reflect.DeepEqual(got, prior) {
				t.Fatal("failed deletion changed persisted records")
			}
		})
	}
}

func TestExportURIRejectsAmbiguousColonNames(t *testing.T) {
	for _, field := range []string{"issuer", "account"} {
		input := testInput("fixture@example.test")
		if field == "issuer" {
			input.Issuer = "Service:Two"
		} else {
			input.Account = "account:two"
		}
		if _, err := ExportURI(input); err == nil || !strings.Contains(err.Error(), "冒号") {
			t.Fatal("ambiguous colon-containing label was exported")
		}
		store, _ := newTestStore(t)
		token, err := store.Add(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ExportURIs(); err == nil {
			t.Fatal("legacy bulk export accepted unimportable label")
		}
		if _, err := store.ExportSelection([]string{token.ID}); err == nil {
			t.Fatal("selected export accepted unimportable label")
		}
	}
}
