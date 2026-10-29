package store

import (
	"database/sql"
	"fmt"
)

// History tables remain account-local. No original export/media is removed by
// history retention or deselection. Triggers invalidate derived data in the
// same transaction as ingestion, even while the embedding worker is stopped.
func installHistorySchema(db *sql.DB) error {
	rows, err := db.Query("PRAGMA table_info(messages)")
	if err != nil {
		return err
	}
	columns := map[string]bool{}
	for rows.Next() {
		var n, ty string
		var id, nn, pk int
		var d any
		if err = rows.Scan(&id, &n, &ty, &nn, &d, &pk); err != nil {
			rows.Close()
			return err
		}
		columns[n] = true
	}
	rows.Close()
	for _, c := range []struct{ name, ddl string }{{"reply_id", "TEXT NOT NULL DEFAULT ''"}, {"reply_chat_jid", "TEXT NOT NULL DEFAULT ''"}, {"deleted", "INTEGER NOT NULL DEFAULT 0"}} {
		if !columns[c.name] {
			if _, err = db.Exec("ALTER TABLE messages ADD COLUMN " + c.name + " " + c.ddl); err != nil {
				return err
			}
		}
	}
	_, err = db.Exec(historySchema)
	return err
}

const historySchema = `
CREATE TABLE IF NOT EXISTS history_meta(key TEXT PRIMARY KEY,value TEXT NOT NULL);
INSERT OR IGNORE INTO history_meta VALUES('mode','none'),('scope_epoch','1'),('content_epoch','1'),('manifest',''),('schema_version','1');
CREATE TABLE IF NOT EXISTS history_sync(chat_jid TEXT PRIMARY KEY,status TEXT NOT NULL,requested_at TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS history_chats(chat_jid TEXT PRIMARY KEY, selected INTEGER NOT NULL DEFAULT 1);
CREATE TABLE IF NOT EXISTS history_dirty(chat_jid TEXT NOT NULL,message_id TEXT NOT NULL,version INTEGER NOT NULL,PRIMARY KEY(chat_jid,message_id));
CREATE TABLE IF NOT EXISTS history_docs(source_id TEXT PRIMARY KEY,chat_jid TEXT NOT NULL,message_id TEXT NOT NULL,kind TEXT NOT NULL,chunk INTEGER NOT NULL,content_hash TEXT NOT NULL,body TEXT NOT NULL,source_json TEXT NOT NULL,ts INTEGER NOT NULL,sender TEXT NOT NULL,vector BLOB NOT NULL,space TEXT NOT NULL);
CREATE INDEX IF NOT EXISTS history_docs_scope ON history_docs(chat_jid,ts,sender);
CREATE VIRTUAL TABLE IF NOT EXISTS history_fts USING fts4(body,tokenize=unicode61);
CREATE TRIGGER IF NOT EXISTS history_doc_delete AFTER DELETE ON history_docs BEGIN DELETE FROM history_fts WHERE docid=old.rowid; END;
CREATE TABLE IF NOT EXISTS history_jobs(id TEXT PRIMARY KEY,kind TEXT NOT NULL,status TEXT NOT NULL,request_json TEXT NOT NULL,dedup TEXT UNIQUE,cursor TEXT NOT NULL DEFAULT '',processed INTEGER NOT NULL DEFAULT 0,total INTEGER NOT NULL DEFAULT 0,result_json TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',created INTEGER NOT NULL,updated INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS history_jobs_status ON history_jobs(status,created);
CREATE TABLE IF NOT EXISTS history_references(id TEXT PRIMARY KEY,chat_jid TEXT NOT NULL,message_id TEXT NOT NULL,source_json TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS history_review_pages(id TEXT PRIMARY KEY,session_id TEXT NOT NULL,cursor_json TEXT NOT NULL,created INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS history_sessions(id TEXT PRIMARY KEY,chats_json TEXT NOT NULL,scope_epoch INTEGER NOT NULL,external_consent INTEGER NOT NULL,created INTEGER NOT NULL,expires INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS history_turns(id INTEGER PRIMARY KEY,session_id TEXT NOT NULL,question TEXT NOT NULL,result_json TEXT NOT NULL,content_epoch INTEGER NOT NULL,created INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS history_turns_session ON history_turns(session_id,id);
CREATE TRIGGER IF NOT EXISTS history_new_chat AFTER INSERT ON chats WHEN (SELECT value FROM history_meta WHERE key='mode')='all' BEGIN INSERT OR IGNORE INTO history_chats(chat_jid) VALUES(new.jid); END;
`

// InstallHistoryTriggers is split out so tests can verify legacy migrations.
func InstallHistoryTriggers(db *sql.DB) error {
	if err := installHistorySchema(db); err != nil {
		return err
	}
	for _, event := range []struct{ name, timing, condition, chat, id string }{
		{"message_insert", "AFTER INSERT ON messages", "EXISTS(SELECT 1 FROM history_chats WHERE chat_jid=new.chat_jid AND selected=1)", "new.chat_jid", "new.id"},
		{"message_update", "AFTER UPDATE ON messages", "EXISTS(SELECT 1 FROM history_chats WHERE chat_jid=new.chat_jid AND selected=1)", "new.chat_jid", "new.id"},
		{"message_delete", "AFTER DELETE ON messages", "EXISTS(SELECT 1 FROM history_chats WHERE chat_jid=old.chat_jid AND selected=1)", "old.chat_jid", "old.id"},
		{"transcript_insert", "AFTER INSERT ON analysis_jobs", "new.task='transcribe' AND EXISTS(SELECT 1 FROM history_chats WHERE chat_jid=new.source_chat_jid AND selected=1)", "new.source_chat_jid", "new.source_message_id"},
		{"transcript_update", "AFTER UPDATE ON analysis_jobs", "new.task='transcribe' AND EXISTS(SELECT 1 FROM history_chats WHERE chat_jid=new.source_chat_jid AND selected=1)", "new.source_chat_jid", "new.source_message_id"},
		{"transcript_delete", "AFTER DELETE ON analysis_jobs", "old.task='transcribe' AND EXISTS(SELECT 1 FROM history_chats WHERE chat_jid=old.source_chat_jid AND selected=1)", "old.source_chat_jid", "old.source_message_id"},
	} {
		q := fmt.Sprintf(`CREATE TRIGGER IF NOT EXISTS history_%s %s WHEN %s BEGIN
 UPDATE history_meta SET value=CAST(value AS INTEGER)+1 WHERE key='content_epoch';
 INSERT INTO history_dirty(chat_jid,message_id,version) VALUES(%s,%s,(SELECT CAST(value AS INTEGER) FROM history_meta WHERE key='content_epoch')) ON CONFLICT(chat_jid,message_id) DO UPDATE SET version=excluded.version;
 DELETE FROM history_docs WHERE chat_jid=%s AND message_id=%s;
 DELETE FROM history_references WHERE chat_jid=%s AND message_id=%s;
 DELETE FROM history_turns;
 UPDATE history_jobs SET request_json=json_set(json_remove(request_json,'$.review'),'$.question',''),result_json='',status='stale',error='Source history changed; request a fresh answer' WHERE kind IN ('ask','actions') AND status NOT IN ('cancelled','stale');
 DELETE FROM analysis_jobs WHERE task='history_answer';
 END;`, event.name, event.timing, event.condition, event.chat, event.id, event.chat, event.id, event.chat, event.id)
		if _, err := db.Exec(q); err != nil {
			return err
		}
	}
	return nil
}
