package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"LumaAuthenticator/internal/vault"
	wailsruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

func exportTestApp(t *testing.T) (*App, []vault.Token) {
	t.Helper()
	if runtime.GOOS != "windows" {
		t.Skip("vault uses Windows DPAPI")
	}
	a := NewApp()
	a.init(t.TempDir())
	if err := a.ready(); err != nil {
		t.Fatal(err)
	}
	var tokens []vault.Token
	for i, secret := range []string{"JBSWY3DPEHPK3PXP", "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ", "KRSXG5A"} {
		token, err := a.AddToken(vault.TokenInput{Issuer: "Example", Account: []string{"first@example.test", "second@example.test", "third@example.test"}[i], Secret: secret})
		if err != nil {
			t.Fatal(err)
		}
		tokens = append(tokens, token)
	}
	return a, tokens
}

func TestSelectedExportCopyThenDeleteExactSnapshot(t *testing.T) {
	a, tokens := exportTestApp(t)
	exported, err := a.PreviewTokenExport([]string{tokens[0].ID, tokens[2].ID})
	if err != nil || len(exported.Credentials) != 2 || exported.ExportID == "" {
		t.Fatalf("selected preview failed: %v", err)
	}
	if err := a.DeleteExportedTokens(exported.ExportID); err == nil {
		t.Fatal("preview alone authorized deletion")
	}
	var clipboard string
	a.clipboardWrite = func(_ context.Context, value string) error { clipboard = value; return nil }
	if err := a.CopyTokenExport(exported.ExportID); err != nil || clipboard != exported.Text {
		t.Fatalf("selected copy failed: %v", err)
	}
	if err := a.DeleteExportedTokens(exported.ExportID); err != nil {
		t.Fatal(err)
	}
	remaining, _ := a.GetTokens()
	if len(remaining) != 1 || remaining[0].ID != tokens[1].ID {
		t.Fatal("export cleanup deleted an unexported account")
	}
	if err := a.DeleteExportedTokens(exported.ExportID); err == nil {
		t.Fatal("completed export session reused")
	}
}

func TestSelectedExportCancelFailuresDiscardAndExpiryNeverAuthorizeDeletion(t *testing.T) {
	for _, mode := range []string{"clipboard failure", "save cancel", "dialog failure", "write failure", "discard", "expired", "replacement", "protected destination"} {
		t.Run(mode, func(t *testing.T) {
			a, tokens := exportTestApp(t)
			exported, err := a.PreviewTokenExport([]string{tokens[0].ID})
			if err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(filepath.Join(a.dataPath, "vault.dat"))
			switch mode {
			case "clipboard failure":
				a.clipboardWrite = func(context.Context, string) error { return errors.New("fixture unavailable") }
				if err := a.CopyTokenExport(exported.ExportID); err == nil {
					t.Fatal("failed clipboard copy reported success")
				}
			case "save cancel":
				a.exportSaveDialog = func(context.Context, wailsruntime.SaveDialogOptions) (string, error) { return "", nil }
				if saved, err := a.SaveTokenExport(exported.ExportID); saved || err != nil {
					t.Fatal("canceled save did not return false")
				}
			case "dialog failure":
				a.exportSaveDialog = func(context.Context, wailsruntime.SaveDialogOptions) (string, error) {
					return "", errors.New("fixture dialog failure")
				}
				if saved, err := a.SaveTokenExport(exported.ExportID); saved || err == nil {
					t.Fatal("dialog failure reported success")
				}
			case "write failure":
				destination := filepath.Join(t.TempDir(), "export.txt")
				if err := os.WriteFile(destination, []byte("existing backup"), 0600); err != nil {
					t.Fatal(err)
				}
				a.exportSaveDialog = func(context.Context, wailsruntime.SaveDialogOptions) (string, error) { return destination, nil }
				a.exportWriteFile = func(string, []byte) error { return errors.New("fixture write failure") }
				if saved, err := a.SaveTokenExport(exported.ExportID); saved || err == nil {
					t.Fatal("write failure reported success")
				}
				if prior, _ := os.ReadFile(destination); string(prior) != "existing backup" {
					t.Fatal("failed write replaced original backup")
				}
			case "discard":
				_ = a.DiscardTokenExport(exported.ExportID)
			case "expired":
				a.tokenExport.expires = time.Now().Add(-time.Minute)
			case "replacement":
				_, _ = a.PreviewTokenExport([]string{tokens[1].ID})
			case "protected destination":
				a.exportSaveDialog = func(context.Context, wailsruntime.SaveDialogOptions) (string, error) {
					return filepath.Join(a.dataPath, "backup.txt"), nil
				}
				if saved, err := a.SaveTokenExport(exported.ExportID); saved || err == nil {
					t.Fatal("export accepted protected destination")
				}
			}
			if err := a.DeleteExportedTokens(exported.ExportID); err == nil {
				t.Fatal("unsuccessful or discarded export authorized deletion")
			}
			after, _ := os.ReadFile(filepath.Join(a.dataPath, "vault.dat"))
			remaining, _ := a.GetTokens()
			if !bytes.Equal(before, after) || len(remaining) != len(tokens) {
				t.Fatal("unsuccessful export modified accounts")
			}
		})
	}
}

func TestSelectedExportNativeSaveCommitsBeforeDeletionAndRejectsChangedRecord(t *testing.T) {
	a, tokens := exportTestApp(t)
	exported, err := a.PreviewTokenExport([]string{tokens[1].ID})
	if err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "export.txt")
	if err := os.WriteFile(destination, []byte("prior backup"), 0600); err != nil {
		t.Fatal(err)
	}
	a.exportSaveDialog = func(context.Context, wailsruntime.SaveDialogOptions) (string, error) { return destination, nil }
	if saved, err := a.SaveTokenExport(exported.ExportID); err != nil || !saved {
		t.Fatalf("native file save failed: %v", err)
	}
	content, _ := os.ReadFile(destination)
	if string(content) != exported.Text {
		t.Fatal("saved file differs from selected export")
	}
	if _, err := a.UpdateToken(tokens[1].ID, vault.TokenInput{Issuer: "Renamed", Account: tokens[1].Account}); err != nil {
		t.Fatal(err)
	}
	if err := a.DeleteExportedTokens(exported.ExportID); err == nil {
		t.Fatal("changed account deleted using stale export")
	}
	remaining, _ := a.GetTokens()
	if len(remaining) != len(tokens) {
		t.Fatal("stale export cleanup deleted accounts")
	}
}

func TestExportDestinationRestrictionsAndAtomicWrite(t *testing.T) {
	dataDir, outDir := t.TempDir(), t.TempDir()
	for _, destination := range []string{filepath.Join(dataDir, "backup.txt"), filepath.Join(dataDir, "vault.dat"), filepath.Join(outDir, "program.exe")} {
		if _, err := exportDestination(destination, dataDir); err == nil {
			t.Fatal("unsafe export destination accepted")
		}
	}
	destination, err := exportDestination(filepath.Join(outDir, "backup"), dataDir)
	if err != nil || filepath.Ext(destination) != ".txt" {
		t.Fatal("ordinary export destination rejected")
	}
	if err := writeExportAtomically(destination, []byte("synthetic backup")); err != nil {
		t.Fatal(err)
	}
	if err := writeExportAtomically(destination, []byte("new synthetic backup")); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(destination)
	if string(content) != "new synthetic backup" {
		t.Fatal("atomic export replacement is incomplete")
	}
	entries, _ := os.ReadDir(outDir)
	if len(entries) != 1 {
		t.Fatal("export leaked a temporary backup file")
	}
}
