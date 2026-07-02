CREATE TABLE memories (
	id TEXT PRIMARY KEY,
	text TEXT NOT NULL,
	created_at TEXT NOT NULL
);

INSERT INTO memories (id, text, created_at)
VALUES ('mem_legacy_sqlite', 'legacy sqlite memory', '2026-06-25T01:02:03Z');
