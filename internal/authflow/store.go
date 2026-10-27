// Package authflow implements the owner's MCP OAuth authorization server.
package authflow

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/go-oauth2/oauth2/v4"
	"github.com/go-oauth2/oauth2/v4/models"
	_ "github.com/mattn/go-sqlite3"
)

func digest(raw string) string {
	if raw == "" {
		return ""
	}
	h := sha256.Sum256([]byte(raw))
	return "sha256:" + hex.EncodeToString(h[:])
}

type clientInfo struct {
	ID        string   `json:"client_id"`
	Name      string   `json:"client_name"`
	Redirects []string `json:"redirect_uris"`
}

func (s *Server) client(ctx context.Context, id string) (*clientInfo, error) {
	var raw []byte
	if err := s.db.QueryRowContext(ctx, "SELECT data FROM clients WHERE id=?", id).Scan(&raw); err != nil {
		return nil, err
	}
	var c clientInfo
	err := json.Unmarshal(raw, &c)
	return &c, err
}
func (s *Server) GetByID(ctx context.Context, id string) (oauth2.ClientInfo, error) {
	c, err := s.client(ctx, id)
	if err != nil {
		return nil, err
	}
	// Domain holds the registered redirect set; exact matching is configured
	// explicitly on the manager instead of its default domain-prefix matching.
	return &models.Client{ID: c.ID, Domain: strings.Join(c.Redirects, "\n"), Public: true}, nil
}

type tokenStore struct{ server *Server }

func (t tokenStore) Create(ctx context.Context, info oauth2.TokenInfo) error {
	raw, err := json.Marshal(info)
	if err != nil {
		return err
	}
	var safe models.Token
	if err = json.Unmarshal(raw, &safe); err != nil {
		return err
	}
	safe.Code = digest(info.GetCode())
	safe.Access = digest(info.GetAccess())
	safe.Refresh = digest(info.GetRefresh())
	raw, err = json.Marshal(safe)
	if err != nil {
		return err
	}
	_, err = t.server.db.ExecContext(ctx, "INSERT INTO tokens(code_hash,access_hash,refresh_hash,grant_id,data) VALUES(NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,?)", safe.Code, safe.Access, safe.Refresh, info.GetUserID(), raw)
	return err
}
func (t tokenStore) lookup(ctx context.Context, column, raw string) (oauth2.TokenInfo, error) {
	var data []byte
	err := t.server.db.QueryRowContext(ctx, "SELECT t.data FROM tokens t JOIN grants g ON g.id=t.grant_id WHERE t."+column+"=? AND g.revoked=0 AND g.expires>? AND g.resource=?", digest(raw), time.Now().Unix(), t.server.Resource).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var info models.Token
	if err = json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	switch column {
	case "code_hash":
		info.Code = raw
	case "access_hash":
		info.Access = raw
	case "refresh_hash":
		info.Refresh = raw
	}
	return &info, nil
}
func (t tokenStore) GetByCode(ctx context.Context, raw string) (oauth2.TokenInfo, error) {
	return t.lookup(ctx, "code_hash", raw)
}
func (t tokenStore) GetByAccess(ctx context.Context, raw string) (oauth2.TokenInfo, error) {
	return t.lookup(ctx, "access_hash", raw)
}
func (t tokenStore) GetByRefresh(ctx context.Context, raw string) (oauth2.TokenInfo, error) {
	return t.lookup(ctx, "refresh_hash", raw)
}
func (t tokenStore) RemoveByCode(ctx context.Context, raw string) error {
	_, err := t.server.db.ExecContext(ctx, "DELETE FROM tokens WHERE code_hash=?", digest(raw))
	return err
}
func (t tokenStore) RemoveByAccess(ctx context.Context, raw string) error {
	// Refresh handling only knows the stored hash of the old access token.
	// This shortcut is intentionally deletion-only: hashes cannot authenticate.
	key := digest(raw)
	if strings.HasPrefix(raw, "sha256:") {
		key = raw
	}
	_, err := t.server.db.ExecContext(ctx, "UPDATE tokens SET access_hash=NULL WHERE access_hash=?", key)
	return err
}
func (t tokenStore) RemoveByRefresh(ctx context.Context, raw string) error {
	tx, err := t.server.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	key := digest(raw)
	_, err = tx.ExecContext(ctx, "INSERT OR IGNORE INTO spent_refresh(hash,grant_id) SELECT refresh_hash,grant_id FROM tokens WHERE refresh_hash=?", key)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM tokens WHERE refresh_hash=?", key)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// Grant is the non-secret connection information shown to the owner.
type Grant struct {
	ID         string `json:"id"`
	ClientName string `json:"client_name"`
	Created    int64  `json:"created_at"`
	Expires    int64  `json:"expires_at"`
}

func (s *Server) Grants(ctx context.Context) ([]Grant, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT g.id,c.data,g.created,g.expires FROM grants g JOIN clients c ON c.id=g.client_id WHERE g.revoked=0 AND g.expires>? ORDER BY g.created DESC", time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Grant{}
	for rows.Next() {
		var g Grant
		var raw []byte
		if err := rows.Scan(&g.ID, &raw, &g.Created, &g.Expires); err != nil {
			return nil, err
		}
		var c clientInfo
		if err = json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		g.ClientName = c.Name
		out = append(out, g)
	}
	return out, rows.Err()
}
func (s *Server) Revoke(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx, "UPDATE grants SET revoked=1 WHERE id=?", id)
	return err
}
