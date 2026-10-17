ALTER TABLE users ADD COLUMN auth_version INTEGER NOT NULL DEFAULT 1;

UPDATE users SET email = lower(trim(email));

CREATE UNIQUE INDEX users_email_case_insensitive
  ON users(email COLLATE NOCASE);
