// Package accounts owns site identities. Usernames can never select a data directory.
package accounts

import (
	"context"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Banana1995/WeiboSpider/backend/internal/database"
)

const OwnerID = "site-owner-v1"
const ownerName = "gaomanyi"
const sessionLife = 7 * 24 * time.Hour
const rounds = 600000

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_.-]{2,31}$`)
var errCredentials = errors.New("credentials rejected")
var ErrPassword = errors.New("密码需要至少 12 个字符，最多 256 个字节")

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

func (u User) Owner() bool { return u.ID == OwnerID }

type Store struct{ *database.DB }

func Open(ctx context.Context, dir string) (*Store, error) {
	db, err := database.Open(ctx, dir, "accounts")
	if err != nil {
		return nil, err
	}
	err = db.WithTx(ctx, func(tx *sql.Tx) error {
		_, e := tx.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS users (
		id TEXT PRIMARY KEY, username TEXT UNIQUE NOT NULL, salt BLOB, verifier BLOB, iterations INTEGER,
		setup_hash BLOB, setup_until INTEGER);
		CREATE TABLE IF NOT EXISTS sessions (digest BLOB PRIMARY KEY, user_id TEXT NOT NULL REFERENCES users(id), expires INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS sessions_expiry ON sessions(expires);`)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO users(id,username) VALUES(?,?) ON CONFLICT(id) DO NOTHING`, OwnerID, ownerName)
		return e
	})
	if err != nil {
		return nil, errors.Join(err, db.Close())
	}
	return &Store{db}, nil
}

func secret() string {
	var b [32]byte
	rand.Read(b[:])
	return base64.RawURLEncoding.EncodeToString(b[:])
}
func digest(s string) []byte { d := sha256.Sum256([]byte(s)); return d[:] }
func derive(password string, salt []byte, n int) ([]byte, error) {
	return pbkdf2.Key(sha256.New, password, salt, n, 32)
}
func normalize(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// Issue runs only from a trusted server terminal. Open the existing database in
// read-write mode; no second server lease, schema creation or first-user claim.
func Issue(ctx context.Context, dir, name string, recovery bool) (string, error) {
	name = normalize(name)
	if !usernamePattern.MatchString(name) {
		return "", errors.New("用户名需为 3–32 位小写字母、数字、点、短横线或下划线")
	}
	p, e := filepath.Abs(filepath.Join(dir, "accounts.db"))
	if e != nil {
		return "", e
	}
	u := url.URL{Scheme: "file", Path: p, RawQuery: "mode=rw&_busy_timeout=5000&_txlock=immediate"}
	db, e := sql.Open("sqlite3", u.String())
	if e != nil {
		return "", e
	}
	defer db.Close()
	tx, e := db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	if !recovery && name != ownerName {
		if _, e = tx.ExecContext(ctx, `INSERT INTO users(id,username) VALUES(?,?) ON CONFLICT(username) DO NOTHING`, secret(), name); e != nil {
			return "", e
		}
	}
	code := secret()
	q := `UPDATE users SET setup_hash=?,setup_until=? WHERE username=?`
	if !recovery {
		q += ` AND verifier IS NULL`
	}
	r, e := tx.ExecContext(ctx, q, digest(code), time.Now().Add(30*time.Minute).Unix(), name)
	if e != nil {
		return "", e
	}
	count, e := r.RowsAffected()
	if e != nil {
		return "", e
	}
	if count != 1 {
		return "", errors.New("账号不存在或已经设置过密码")
	}
	if e = tx.Commit(); e != nil {
		return "", e
	}
	return "/login#" + url.Values{"setup": {code}, "user": {name}}.Encode(), nil
}

func (s *Store) activate(ctx context.Context, name, code, password string) (User, string, error) {
	if utf8.RuneCountInString(password) < 12 || len(password) > 256 {
		return User{}, "", ErrPassword
	}
	if len(code) != 43 {
		return User{}, "", errCredentials
	}
	var id string
	e := s.QueryRowContext(ctx, `SELECT id FROM users WHERE username=? AND setup_hash=? AND setup_until>?`, name, digest(code), time.Now().Unix()).Scan(&id)
	if errors.Is(e, sql.ErrNoRows) {
		e = errCredentials
	}
	if e != nil {
		return User{}, "", e
	}
	salt := make([]byte, 16)
	rand.Read(salt)
	hash, e := derive(password, salt, rounds)
	if e != nil {
		return User{}, "", e
	}
	token := secret()
	e = s.WithTx(ctx, func(tx *sql.Tx) error {
		r, e := tx.ExecContext(ctx, `UPDATE users SET salt=?,verifier=?,iterations=?,setup_hash=NULL,setup_until=NULL WHERE id=? AND setup_hash=? AND setup_until>?`, salt, hash, rounds, id, digest(code), time.Now().Unix())
		if e != nil {
			return e
		}
		n, e := r.RowsAffected()
		if e != nil {
			return e
		}
		if n != 1 {
			return errCredentials
		}
		if _, e = tx.ExecContext(ctx, `DELETE FROM sessions WHERE user_id=?`, id); e != nil {
			return e
		}
		return addSession(ctx, tx, id, token)
	})
	return User{id, name}, token, e
}

func addSession(ctx context.Context, tx *sql.Tx, id, token string) error {
	if _, e := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires<=?`, time.Now().Unix()); e != nil {
		return e
	}
	_, e := tx.ExecContext(ctx, `INSERT INTO sessions(digest,user_id,expires) VALUES(?,?,?)`, digest(token), id, time.Now().Add(sessionLife).Unix())
	return e
}

func (s *Store) login(ctx context.Context, name, password string) (User, string, error) {
	var id string
	var salt, want []byte
	var n int
	e := s.QueryRowContext(ctx, `SELECT id,salt,verifier,iterations FROM users WHERE username=? AND verifier IS NOT NULL`, name).Scan(&id, &salt, &want, &n)
	if errors.Is(e, sql.ErrNoRows) {
		salt = make([]byte, 16)
		want = make([]byte, 32)
		n = rounds
	} else if e != nil {
		return User{}, "", e
	}
	if n != rounds {
		return User{}, "", fmt.Errorf("unsupported password verifier")
	}
	actual, e := derive(password, salt, n)
	if e != nil {
		return User{}, "", e
	}
	if subtle.ConstantTimeCompare(actual, want) != 1 || id == "" {
		return User{}, "", errCredentials
	}
	token := secret()
	e = s.WithTx(ctx, func(tx *sql.Tx) error {
		// A concurrent password reset must not resurrect the old credential.
		var current []byte
		if e := tx.QueryRowContext(ctx, `SELECT verifier FROM users WHERE id=?`, id).Scan(&current); e != nil {
			return e
		}
		if subtle.ConstantTimeCompare(want, current) != 1 {
			return errCredentials
		}
		return addSession(ctx, tx, id, token)
	})
	return User{id, name}, token, e
}

func (s *Store) lookup(ctx context.Context, token string) (User, error) {
	var u User
	if len(token) != 43 {
		return u, errCredentials
	}
	e := s.QueryRowContext(ctx, `SELECT u.id,u.username FROM users u JOIN sessions s ON s.user_id=u.id WHERE s.digest=? AND s.expires>?`, digest(token), time.Now().Unix()).Scan(&u.ID, &u.Username)
	if errors.Is(e, sql.ErrNoRows) {
		e = errCredentials
	}
	return u, e
}
