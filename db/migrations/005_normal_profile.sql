UPDATE beemo_code.sessions SET mode = 'normal' WHERE mode = 'chat';
ALTER TABLE beemo_code.sessions ALTER COLUMN mode SET DEFAULT 'normal';
