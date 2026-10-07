package main

import (
	"context"
	"errors"
	"runtime"
	"testing"

	"LumaAuthenticator/internal/vault"
)

func TestCopyAccountUsesSavedMetadataAndPreservesClipboardErrors(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("vault uses Windows DPAPI")
	}
	a := NewApp()
	a.init(t.TempDir())
	if err := a.ready(); err != nil {
		t.Fatal(err)
	}
	input := vault.TokenInput{Issuer: "OpenAI", Account: "copy-fixture@example.com", Secret: "JBSWY3DPEHPK3PXP"}
	token, err := a.AddToken(input)
	if err != nil {
		t.Fatal(err)
	}
	var copied []string
	a.clipboardWrite = func(_ context.Context, text string) error {
		copied = append(copied, text)
		return nil
	}
	if err := a.CopyAccount(token.ID); err != nil || len(copied) != 1 || copied[0] != input.Account {
		t.Fatalf("wrong copied account: %v, %v", copied, err)
	}
	input.Account, input.Secret = "renamed-fixture@example.com", ""
	if _, err := a.UpdateToken(token.ID, input); err != nil {
		t.Fatal(err)
	}
	if err := a.CopyAccount(token.ID); err != nil || len(copied) != 2 || copied[1] != input.Account {
		t.Fatalf("copy used stale account: %v, %v", copied, err)
	}
	if err := a.CopyAccount("missing"); !errors.Is(err, vault.ErrNotFound) || len(copied) != 2 {
		t.Fatal("unknown account wrote to clipboard")
	}
	failed := errors.New("synthetic clipboard unavailable")
	a.clipboardWrite = func(context.Context, string) error { return failed }
	if err := a.CopyAccount(token.ID); !errors.Is(err, failed) {
		t.Fatalf("clipboard failure was hidden: %v", err)
	}
}

func TestCopyAccountRejectsUninitializedApp(t *testing.T) {
	a := NewApp()
	a.clipboardWrite = func(context.Context, string) error {
		t.Fatal("uninitialized application wrote clipboard")
		return nil
	}
	if err := a.CopyAccount("missing"); err == nil {
		t.Fatal("uninitialized copy succeeded")
	}
}
