package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCredentialRoundTrip(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()

	meta, err := s.SaveCredential(ctx, "c1", "homelab key", CredentialSSHKey, []byte("-----BEGIN KEY-----\nabc"))
	if err != nil {
		t.Fatalf("save: %v", err)
	}
	if !meta.HasSecret || meta.Name != "homelab key" {
		t.Fatalf("meta wrong: %+v", meta)
	}

	secret, err := s.CredentialSecret(ctx, "c1")
	if err != nil {
		t.Fatalf("get secret: %v", err)
	}
	if string(secret) != "-----BEGIN KEY-----\nabc" {
		t.Fatalf("secret corrupted: %q", secret)
	}

	list, err := s.ListCredentials(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) != 1 || list[0].Kind != CredentialSSHKey {
		t.Fatalf("list wrong: %+v", list)
	}
}

func TestCredentialEncryptedAtRest(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	keyPath := filepath.Join(dir, "secret.key")

	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := s.SaveCredential(context.Background(), "c1", "k", CredentialAPIKey, []byte("super-secret-value")); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.Close()

	// The raw database file must not contain the plaintext secret.
	raw, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatalf("read db: %v", err)
	}
	if strings.Contains(string(raw), "super-secret-value") {
		t.Fatal("plaintext secret found in database file")
	}

	// Key file must exist with restrictive permissions.
	fi, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("stat key: %v", err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file perms = %o", fi.Mode().Perm())
	}

	// Reopening with the same key must decrypt successfully.
	s2, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	secret, err := s2.CredentialSecret(context.Background(), "c1")
	if err != nil {
		t.Fatalf("decrypt after reopen: %v", err)
	}
	if string(secret) != "super-secret-value" {
		t.Fatalf("secret = %q", secret)
	}

	// A different key must fail to decrypt.
	s3, err := Open(dbPath, filepath.Join(dir, "other.key"))
	if err != nil {
		t.Fatalf("open other key: %v", err)
	}
	defer s3.Close()
	if _, err := s3.CredentialSecret(context.Background(), "c1"); err == nil {
		t.Fatal("expected decryption failure with wrong key")
	}
}

func TestCredentialValidation(t *testing.T) {
	s := openTest(t)
	ctx := context.Background()
	if _, err := s.SaveCredential(ctx, "c1", "", CredentialAPIKey, []byte("x")); err == nil {
		t.Fatal("expected name error")
	}
	if _, err := s.SaveCredential(ctx, "c1", "n", CredentialAPIKey, nil); err == nil {
		t.Fatal("expected secret error")
	}
}
