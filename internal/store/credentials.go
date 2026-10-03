package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Credential kinds supported by v0.1. The set is open-ended; deployment
// backends and integrations interpret kinds, the store does not.
const (
	CredentialSSHKey         = "ssh_key"
	CredentialSSHPassword    = "ssh_password"
	CredentialBecomePassword = "become_password"
	CredentialAPIKey         = "api_key"
	CredentialS3             = "s3"
)

// CredentialMeta is the only representation exposed over the API. Secret
// material is never returned after storage.
type CredentialMeta struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	HasSecret bool      `json:"hasSecret"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// secretBox encrypts secrets at rest with AES-GCM using a random key held
// in a 0600 key file next to the database. This uses only standard
// library primitives (no custom cryptography).
//
// Threat model, explicitly documented: this protects database files at
// rest (backups, copies, accidental exposure). An attacker who can read
// both the database and the key file — i.e. anyone with the same local
// filesystem permissions as the Visualible process — can decrypt. This
// is the same trust boundary the application itself operates under.
type secretBox struct {
	keyPath string
	aead    cipher.AEAD
}

func openSecretBox(keyPath string) (*secretBox, error) {
	key, err := os.ReadFile(keyPath)
	if errors.Is(err, os.ErrNotExist) {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate secret key: %w", err)
		}
		if err := os.WriteFile(keyPath, key, 0o600); err != nil {
			return nil, fmt.Errorf("write secret key: %w", err)
		}
	} else if err != nil {
		return nil, err
	} else if len(key) != 32 {
		return nil, fmt.Errorf("secret key file %s has invalid length %d", keyPath, len(key))
	}
	aead, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	return &secretBox{keyPath: keyPath, aead: aead}, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func (b *secretBox) seal(plaintext []byte) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, nil), nil
}

func (b *secretBox) open(blob []byte) ([]byte, error) {
	ns := b.aead.NonceSize()
	if len(blob) < ns {
		return nil, fmt.Errorf("ciphertext too short")
	}
	return b.aead.Open(nil, blob[:ns], blob[ns:], nil)
}

const credentialsSchema = `
CREATE TABLE IF NOT EXISTS credentials (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    kind       TEXT NOT NULL,
    secret     BLOB NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_credentials_name ON credentials(name);
`

// SaveCredential inserts or replaces a credential. The secret is
// encrypted before it touches the database and is never logged.
func (s *Store) SaveCredential(ctx context.Context, id, name, kind string, secret []byte) (*CredentialMeta, error) {
	if name == "" || kind == "" {
		return nil, fmt.Errorf("credential name and kind are required")
	}
	if len(secret) == 0 {
		return nil, fmt.Errorf("credential secret must not be empty")
	}
	if s.box == nil {
		return nil, fmt.Errorf("secret storage is not initialized")
	}
	blob, err := s.box.seal(secret)
	if err != nil {
		return nil, fmt.Errorf("encrypt secret: %w", err)
	}
	now := time.Now().UTC()
	created := now
	var existing string
	err = s.db.QueryRowContext(ctx, `SELECT created_at FROM credentials WHERE id = ?`, id).Scan(&existing)
	if err == nil {
		created, _ = time.Parse(timeFormat, existing)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO credentials (id, name, kind, secret, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET name = excluded.name, kind = excluded.kind,
		     secret = excluded.secret, updated_at = excluded.updated_at`,
		id, name, kind, blob, created.Format(timeFormat), now.Format(timeFormat))
	if err != nil {
		return nil, err
	}
	return &CredentialMeta{ID: id, Name: name, Kind: kind, HasSecret: true, CreatedAt: created, UpdatedAt: now}, nil
}

// ListCredentials returns metadata for all credentials (never secrets).
func (s *Store) ListCredentials(ctx context.Context) ([]CredentialMeta, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, kind, created_at, updated_at FROM credentials ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CredentialMeta{}
	for rows.Next() {
		var m CredentialMeta
		var created, updated string
		if err := rows.Scan(&m.ID, &m.Name, &m.Kind, &created, &updated); err != nil {
			return nil, err
		}
		m.HasSecret = true
		m.CreatedAt, _ = time.Parse(timeFormat, created)
		m.UpdatedAt, _ = time.Parse(timeFormat, updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// CredentialKind returns the kind of a credential without its secret.
func (s *Store) CredentialKind(ctx context.Context, id string) (string, error) {
	var kind string
	err := s.db.QueryRowContext(ctx, `SELECT kind FROM credentials WHERE id = ?`, id).Scan(&kind)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("credential %s: %w", id, ErrNotFound)
	}
	return kind, err
}

// CredentialSecret decrypts and returns the secret. This is for internal
// consumers (deployment backends) only; HTTP handlers must never expose
// the returned bytes.
func (s *Store) CredentialSecret(ctx context.Context, id string) ([]byte, error) {
	if s.box == nil {
		return nil, fmt.Errorf("secret storage is not initialized")
	}
	var blob []byte
	err := s.db.QueryRowContext(ctx, `SELECT secret FROM credentials WHERE id = ?`, id).Scan(&blob)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("credential %s: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, err
	}
	plain, err := s.box.open(blob)
	if err != nil {
		return nil, fmt.Errorf("decrypt credential %s: %w", id, err)
	}
	return plain, nil
}

// DeleteCredential removes a credential.
func (s *Store) DeleteCredential(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM credentials WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("credential %s: %w", id, ErrNotFound)
	}
	return nil
}

// SecretKeyPath returns the default key file location for a data dir.
func SecretKeyPath(dataDir string) string {
	return filepath.Join(dataDir, "secret.key")
}
