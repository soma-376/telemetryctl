package desktopinstall

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/your-org/pulsemetry/internal/credential"
	"github.com/your-org/pulsemetry/internal/installer"
)

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestRemoveFilesPreservesChangedAndUnregisteredFiles(t *testing.T) {
	home := testHome(t)
	receipt := ReceiptPath(home)
	a := filepath.Join(home, "apps", "gui")
	b := filepath.Join(home, "apps", "daemon")
	extra := filepath.Join(home, "apps", "notes.txt")
	writeFixture(t, a, "gui")
	writeFixture(t, b, "daemon")
	writeFixture(t, extra, "user")
	if err := RegisterFiles(receipt, "gui", []string{a}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterFiles(receipt, "cli", []string{b}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, b, "user modified")
	preserved, err := RemoveFiles(receipt, "gui", "cli")
	if err != nil || len(preserved) != 1 || preserved[0] != b {
		t.Fatalf("%v %v", preserved, err)
	}
	if _, err := os.Stat(a); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("GUI was not removed")
	}
	for _, p := range []string{b, extra} {
		if _, err := os.Stat(p); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := RemoveFiles(receipt, "gui", "cli"); err != nil {
		t.Fatal("retry:", err)
	}
}

func TestReceiptRejectsLinkBeforeDeletingAnyFile(t *testing.T) {
	home := testHome(t)
	receipt := ReceiptPath(home)
	original := filepath.Join(home, "original")
	other := filepath.Join(home, "other")
	writeFixture(t, original, "program")
	writeFixture(t, other, "private")
	if err := RegisterFiles(receipt, "gui", []string{original}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(original); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, original); err != nil {
		t.Skip("symlinks unavailable")
	}
	if _, err := RemoveFiles(receipt, "gui"); err == nil {
		t.Fatal("accepted symlink")
	}
	b, _ := os.ReadFile(other)
	if string(b) != "private" {
		t.Fatal("followed link")
	}
}

func TestCleanupFailureKeepsProgramAndCredentials(t *testing.T) {
	home := testHome(t)
	state := filepath.Join(home, "state.json")
	data := filepath.Join(home, "data")
	writeFixture(t, state, `{"state_schema_version":5,"installation_id":"test-installation","local":{"enabled":false,"store_content":true}}`)
	writeFixture(t, installer.ManagedPath(state), `{"version":1,"installation_id":"test-installation","targets":[]}`)
	program := filepath.Join(home, "program")
	writeFixture(t, program, "program")
	if err := RegisterFiles(ReceiptPath(home), "gui", []string{program}); err != nil {
		t.Fatal(err)
	}
	deleted := false
	result := Remove(context.Background(), home, Options{StatePath: state, DataDir: data, Stop: func(context.Context) error { return errors.New("stop failed") }, DeleteCredential: func(credential.Account) error { deleted = true; return nil }})
	if result.Success || result.Error == "" || deleted {
		t.Fatalf("%+v, credential deletion %v", result, deleted)
	}
	if _, err := os.Stat(program); err != nil {
		t.Fatal("deleted program before cleanup succeeded")
	}
}

func TestCleanupOnlyRemovesDataWhenSelected(t *testing.T) {
	for _, deleteData := range []bool{false, true} {
		t.Run(map[bool]string{false: "preserve", true: "delete"}[deleteData], func(t *testing.T) {
			home := testHome(t)
			state := filepath.Join(home, "state.json")
			data := filepath.Join(home, "data")
			writeFixture(t, state, `{"state_schema_version":5,"installation_id":"test-installation","local":{"enabled":false,"store_content":true}}`)
			writeFixture(t, installer.ManagedPath(state), `{"version":1,"installation_id":"test-installation","targets":[]}`)
			db := filepath.Join(data, "pulsemetry.db")
			writeFixture(t, db, "fake database")
			extra := filepath.Join(data, "user.txt")
			writeFixture(t, extra, "keep")
			result := Remove(context.Background(), home, Options{StatePath: state, DataDir: data, DeleteData: deleteData, Stop: func(context.Context) error { return nil }, DeleteCredential: func(credential.Account) error { return nil }})
			if !result.Success {
				t.Fatal(result.Error)
			}
			_, err := os.Stat(db)
			if deleteData != errors.Is(err, os.ErrNotExist) {
				t.Fatalf("delete=%v err=%v", deleteData, err)
			}
			if _, err := os.Stat(extra); err != nil {
				t.Fatal("removed user file")
			}
		})
	}
}

func testHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return home
}

func TestMissingStateStillStopsDaemonBeforeProgramRemoval(t *testing.T) {
	home := testHome(t)
	program := filepath.Join(home, "program")
	writeFixture(t, program, "program")
	if err := RegisterFiles(ReceiptPath(home), "gui", []string{program}); err != nil {
		t.Fatal(err)
	}
	stopped := false
	result := Remove(context.Background(), home, Options{Stop: func(context.Context) error {
		if _, err := os.Stat(program); err != nil {
			t.Fatal("program removed before stop")
		}
		stopped = true
		return nil
	}, DeleteCredential: func(credential.Account) error { t.Fatal("no installation credentials expected"); return nil }})
	if !stopped || !result.Success {
		t.Fatalf("stopped=%v result=%+v", stopped, result)
	}
	if _, err := os.Stat(program); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("program remains")
	}
}

func TestRegistrationRejectsDuplicateOwnership(t *testing.T) {
	home := testHome(t)
	program := filepath.Join(home, "program")
	writeFixture(t, program, "program")
	if err := RegisterFiles(ReceiptPath(home), "gui", []string{program}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterFiles(ReceiptPath(home), "cli", []string{program}); err == nil {
		t.Fatal("accepted duplicate ownership")
	}
	if _, err := LoadReceipt(ReceiptPath(home)); err != nil {
		t.Fatal("failed registration damaged receipt:", err)
	}
}

func TestFinalizeKeepsModifiedHelperAndAllowsRetry(t *testing.T) {
	home := testHome(t)
	helper := filepath.Join(home, "helper")
	writeFixture(t, helper, "original")
	if err := RegisterFiles(ReceiptPath(home), "uninstaller", []string{helper}); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, helper, "modified")
	if err := finalize(home, func() error { return nil }); err == nil {
		t.Fatal("accepted modified helper")
	}
	writeFixture(t, helper, "original")
	if err := finalize(home, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{helper, ReceiptPath(home)} {
		if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("not removed: %s", name)
		}
	}
}
