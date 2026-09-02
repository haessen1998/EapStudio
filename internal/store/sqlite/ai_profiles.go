package sqlite

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type AIProfile struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Provider  string `json:"provider"`
	BaseURL   string `json:"baseURL"`
	Model     string `json:"model"`
	APIKey    string `json:"-"`
	HasAPIKey bool   `json:"hasApiKey"`
	IsDefault bool   `json:"isDefault"`
}

func (s *Store) SaveAIProfiles(ctx context.Context, profiles []AIProfile, defaultID string) error {
	if len(profiles) == 0 {
		return fmt.Errorf("at least one AI profile is required")
	}
	known := make(map[string]bool, len(profiles))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, profile := range profiles {
		if profile.ID == "" || profile.Name == "" || profile.Provider == "" {
			return fmt.Errorf("AI profile id, name, and provider are required")
		}
		known[profile.ID] = true
		var encrypted, nonce []byte
		if profile.APIKey != "" {
			encrypted, nonce, err = s.encryptSecret(profile.APIKey)
			if err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO ai_profiles
			(id,name,provider,base_url,model,api_key_cipher,api_key_nonce,is_default,updated_at)
			VALUES(?,?,?,?,?,?,?,?,?)
			ON CONFLICT(id) DO UPDATE SET name=excluded.name,provider=excluded.provider,
			base_url=excluded.base_url,model=excluded.model,
			api_key_cipher=COALESCE(excluded.api_key_cipher,ai_profiles.api_key_cipher),
			api_key_nonce=COALESCE(excluded.api_key_nonce,ai_profiles.api_key_nonce),
			is_default=excluded.is_default,updated_at=excluded.updated_at`,
			profile.ID, profile.Name, profile.Provider, profile.BaseURL, profile.Model,
			nullBytes(encrypted), nullBytes(nonce), profile.ID == defaultID, time.Now().UTC())
		if err != nil {
			return err
		}
	}
	if !known[defaultID] {
		return fmt.Errorf("default AI profile %q does not exist", defaultID)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id FROM ai_profiles`)
	if err != nil {
		return err
	}
	var remove []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		if !known[id] {
			remove = append(remove, id)
		}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range remove {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ai_profiles WHERE id=?`, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) AIProfiles(ctx context.Context) ([]AIProfile, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,provider,base_url,model,api_key_cipher,api_key_nonce,is_default
		FROM ai_profiles ORDER BY is_default DESC, updated_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []AIProfile
	for rows.Next() {
		var value AIProfile
		var encrypted, nonce []byte
		if err := rows.Scan(&value.ID, &value.Name, &value.Provider, &value.BaseURL, &value.Model, &encrypted, &nonce, &value.IsDefault); err != nil {
			return nil, err
		}
		value.HasAPIKey = len(encrypted) > 0
		if value.HasAPIKey {
			value.APIKey, err = s.decryptSecret(encrypted, nonce)
			if err != nil {
				return nil, fmt.Errorf("decrypt AI profile %q: %w", value.ID, err)
			}
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) encryptSecret(value string) ([]byte, []byte, error) {
	aead, err := s.secretAEAD()
	if err != nil {
		return nil, nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, fmt.Errorf("generate API key nonce: %w", err)
	}
	return aead.Seal(nil, nonce, []byte(value), nil), nonce, nil
}

func (s *Store) decryptSecret(encrypted, nonce []byte) (string, error) {
	aead, err := s.secretAEAD()
	if err != nil {
		return "", err
	}
	value, err := aead.Open(nil, nonce, encrypted, nil)
	if err != nil {
		return "", fmt.Errorf("AES-GCM authentication failed: %w", err)
	}
	return string(value), nil
}

func (s *Store) secretAEAD() (cipher.AEAD, error) {
	secretPath := filepath.Join(filepath.Dir(s.path), "ai.key")
	secret, err := os.ReadFile(secretPath)
	if os.IsNotExist(err) {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, fmt.Errorf("generate installation secret: %w", err)
		}
		if err := os.WriteFile(secretPath, secret, 0o600); err != nil {
			return nil, fmt.Errorf("store installation secret: %w", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("read installation secret: %w", err)
	}
	key := sha256.Sum256(secret)
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func nullBytes(value []byte) any {
	if len(value) == 0 {
		return nil
	}
	return value
}
