package store

import (
	"database/sql"
	"strings"
)

// contactNameExpr coalesces the whatsmeow_contacts name columns in preference
// order: the saved address-book name first, then push name, first name, and
// business name. NULLIF folds empty strings into NULL so COALESCE skips them.
const contactNameExpr = `COALESCE(NULLIF(full_name,''), NULLIF(push_name,''), NULLIF(first_name,''), NULLIF(business_name,''))`

// ContactName returns the synced address-book name for a JID from the
// whatsmeow_contacts table, or "" when the contact is unknown or the
// whatsmeow store is unavailable. LID JIDs are resolved to phone JIDs first.
func (s *Store) ContactName(jid string) string {
	if s.whatsmeowDB == nil {
		return ""
	}
	resolved := s.ResolveLIDToJID(jid)
	for index, candidate := range []string{resolved, jid} {
		if index == 1 && candidate == resolved {
			continue
		}
		var name sql.NullString
		if s.whatsmeowDB.QueryRow(`SELECT `+contactNameExpr+` FROM whatsmeow_contacts WHERE their_jid=?`, candidate).Scan(&name) == nil && name.Valid && strings.TrimSpace(name.String) != "" {
			return strings.TrimSpace(name.String)
		}
	}
	// Some synced contact names are keyed by LID even after a chat resolves to a phone JID.
	if strings.HasSuffix(resolved, "@s.whatsapp.net") {
		var name sql.NullString
		if s.whatsmeowDB.QueryRow(`SELECT `+contactNameExpr+` FROM whatsmeow_contacts JOIN whatsmeow_lid_map m ON their_jid=m.lid || '@lid' WHERE m.pn=? AND (`+contactNameExpr+`) IS NOT NULL LIMIT 1`, jidUser(resolved)).Scan(&name) == nil && name.Valid {
			return strings.TrimSpace(name.String)
		}
	}
	return ""
}

// searchContactNames returns address-book contacts whose name or JID matches
// the LIKE pattern, as a jid -> display-name map. Empty when the whatsmeow
// store is unavailable.
func (s *Store) searchContactNames(pattern string) map[string]string {
	out := map[string]string{}
	if s.whatsmeowDB == nil {
		return out
	}
	rows, err := s.whatsmeowDB.Query(`
		SELECT their_jid, `+contactNameExpr+` AS name
		FROM whatsmeow_contacts
		WHERE their_jid LIKE '%@s.whatsapp.net'
		  AND (LOWER(full_name) LIKE LOWER(?) OR LOWER(push_name) LIKE LOWER(?)
		    OR LOWER(first_name) LIKE LOWER(?) OR LOWER(business_name) LIKE LOWER(?)
		    OR their_jid LIKE ?)
		LIMIT 50`, pattern, pattern, pattern, pattern, pattern)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var jid string
		var name sql.NullString
		if err := rows.Scan(&jid, &name); err != nil {
			continue
		}
		if name.Valid && name.String != "" {
			out[jid] = name.String
		}
	}
	return out
}

// Resolve names at read time so an early numeric cache entry does not hide
// a contact name synced later. Never substitute a member name for a group.
func (s *Store) enrichChatName(c *Chat) {
	if strings.HasSuffix(c.JID, "@s.whatsapp.net") || strings.HasSuffix(c.JID, "@lid") {
		if name := s.ContactName(c.JID); name != "" {
			c.Name = name
		}
	}
}
