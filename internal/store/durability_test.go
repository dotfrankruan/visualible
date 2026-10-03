package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestDataSurvivesWithoutWALSidecar is the regression test for state that
// "disappears" after a restart: committed data must live in the main .db
// file once the application closes, so copying or backing up only
// visualible.db (without -wal/-shm sidecars) keeps everything.
func TestDataSurvivesWithoutWALSidecar(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "visualible.db")
	keyPath := filepath.Join(dir, "secret.key")

	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if _, err := s.SaveCredential(ctx, "c1", "homelab", CredentialSSHKey, []byte("KEY")); err != nil {
		t.Fatalf("save credential: %v", err)
	}
	if err := s.SaveProject(ctx, demoProject()); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Simulate a naive backup: copy ONLY the main database file.
	backupDir := t.TempDir()
	backupDB := filepath.Join(backupDir, "visualible.db")
	copyFile(t, dbPath, backupDB)
	copyFile(t, keyPath, filepath.Join(backupDir, "secret.key"))

	s2, err := Open(backupDB, filepath.Join(backupDir, "secret.key"))
	if err != nil {
		t.Fatalf("reopen copy: %v", err)
	}
	defer s2.Close()

	creds, err := s2.ListCredentials(ctx)
	if err != nil {
		t.Fatalf("list credentials: %v", err)
	}
	if len(creds) != 1 || creds[0].Name != "homelab" {
		t.Fatalf("credentials lost in the main .db file: %+v", creds)
	}
	if _, err := s2.CredentialSecret(ctx, "c1"); err != nil {
		t.Fatalf("secret not readable from the copy: %v", err)
	}
	if _, err := s2.GetProject(ctx, "p1"); err != nil {
		t.Fatalf("project lost in the main .db file: %v", err)
	}
}

// TestDataSurvivesMissingWALSidecar covers the hostile variant: the WAL
// sidecars are gone entirely (cleaners, sync tools, manual tidying) after
// a clean shutdown. Everything must still be there.
func TestDataSurvivesMissingWALSidecar(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "visualible.db")
	keyPath := filepath.Join(dir, "secret.key")

	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if _, err := s.SaveCredential(ctx, "c1", "lab", CredentialSSHPassword, []byte("pw")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	// Remove any sidecar files a cleanup tool might have taken.
	for _, suffix := range []string{"-wal", "-shm"} {
		_ = os.Remove(dbPath + suffix)
	}

	s2, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	secret, err := s2.CredentialSecret(ctx, "c1")
	if err != nil {
		t.Fatalf("secret lost without the WAL sidecar: %v", err)
	}
	if string(secret) != "pw" {
		t.Fatalf("secret = %q", secret)
	}
}

// TestDataSurvivesUncleanExit simulates a crash: the process is killed
// without Close, leaving the WAL behind. Committed transactions must be
// replayed on the next open.
func TestDataSurvivesUncleanExit(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "visualible.db")
	keyPath := filepath.Join(dir, "secret.key")

	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	ctx := context.Background()
	if _, err := s.SaveCredential(ctx, "c1", "unclean", CredentialAPIKey, []byte("sk-1")); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Deliberately no Close(): the database connection is abandoned, as it
	// would be after SIGKILL. The WAL file must still be on disk.
	if _, err := os.Stat(dbPath + "-wal"); err != nil {
		t.Fatalf("expected a WAL sidecar before recovery: %v", err)
	}

	s2, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("reopen after unclean exit: %v", err)
	}
	defer s2.Close()
	secret, err := s2.CredentialSecret(ctx, "c1")
	if err != nil {
		t.Fatalf("committed credential lost after unclean exit: %v", err)
	}
	if string(secret) != "sk-1" {
		t.Fatalf("secret = %q", secret)
	}
}

// TestNewDatabaseIsReported makes the "wrong data directory" case
// detectable instead of mysterious.
func TestNewDatabaseIsReported(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "visualible.db")

	s, err := Open(dbPath, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if !s.NewDatabase() {
		t.Error("creating a database must be reported as a new database")
	}
	if s.Path() != dbPath {
		t.Errorf("path = %q, want %q", s.Path(), dbPath)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Reopening an existing database is not "new".
	s2, err := Open(dbPath, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	if s2.NewDatabase() {
		t.Error("existing database reported as new")
	}
}

// TestLostKeyFileIsDetected covers the silent-corruption footgun: if
// secret.key is gone, regenerating it makes stored credentials
// undecryptable. The store must flag that the key was recreated, and the
// credentials must fail loudly rather than silently returning garbage.
func TestLostKeyFileIsDetected(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "visualible.db")
	keyPath := filepath.Join(dir, "secret.key")
	ctx := context.Background()

	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.SaveCredential(ctx, "c1", "lab", CredentialSSHKey, []byte("KEY-MATERIAL")); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	if err := os.Remove(keyPath); err != nil {
		t.Fatalf("remove key: %v", err)
	}

	s2, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("reopen with missing key: %v", err)
	}
	defer s2.Close()
	if s2.box == nil || !s2.box.keyCreated {
		t.Fatal("a regenerated key must be recorded so the condition is reported")
	}
	// Metadata still lists (so the user can see what existed)...
	creds, err := s2.ListCredentials(ctx)
	if err != nil || len(creds) != 1 {
		t.Fatalf("credentials metadata lost: %v %+v", err, creds)
	}
	// ...but the secret cannot be decrypted, and says so.
	if _, err := s2.CredentialSecret(ctx, "c1"); err == nil {
		t.Fatal("expected decryption failure after key loss, got nil error")
	} else if !strings.Contains(err.Error(), "decrypt") {
		t.Fatalf("unhelpful error after key loss: %v", err)
	}
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}
