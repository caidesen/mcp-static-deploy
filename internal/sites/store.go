package sites

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	migrationVersion = "0001_sites"
	noteMigration    = "0002_site_note"
	// MaxFileBytes is the largest file a site will store.
	MaxFileBytes = 8 << 20
	// MaxNoteRunes is the longest site description.
	MaxNoteRunes = 160
)

// ErrNotFound is returned when a site or file does not exist for this caller.
var ErrNotFound = errors.New("not found")

// ErrTooLarge is returned when a file exceeds MaxFileBytes.
var ErrTooLarge = errors.New("file too large")

// ErrNoteTooLong is returned when a site note exceeds MaxNoteRunes.
var ErrNoteTooLong = errors.New("note too long")

// Site is one published static site. Files are not stored here.
type Site struct {
	ID          string
	Slug        string
	OwnerUserID string
	Note        string
	CreatedAt   time.Time
}

// File is one file under a site root.
type File struct {
	Path string
	Size int64
}

// Store keeps site rows in SQLite and files on disk.
type Store struct {
	db      *sql.DB
	dataDir string
}

// New binds a store to an already migrated database.
func New(db *sql.DB, dataDir string) *Store {
	return &Store{db: db, dataDir: dataDir}
}

// Migrate creates the sites table. theauth migrations are separate.
func Migrate(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS app_schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("sites: migration ledger: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS sites (
			id TEXT PRIMARY KEY,
			slug TEXT NOT NULL UNIQUE,
			owner_user_id TEXT NOT NULL,
			note TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL
		)`); err != nil {
		return fmt.Errorf("sites: create table: %w", err)
	}
	hasNote, err := hasColumn(ctx, db, "sites", "note")
	if err != nil {
		return err
	}
	if !hasNote {
		if _, err := db.ExecContext(ctx, `ALTER TABLE sites ADD COLUMN note TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("sites: add note: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, `
		CREATE INDEX IF NOT EXISTS sites_owner_idx ON sites (owner_user_id, created_at)`); err != nil {
		return fmt.Errorf("sites: owner index: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT OR IGNORE INTO app_schema_migrations (version, applied_at) VALUES (?, ?)`,
		migrationVersion, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("sites: record migration: %w", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT OR IGNORE INTO app_schema_migrations (version, applied_at) VALUES (?, ?)`,
		noteMigration, time.Now().UTC().Format(time.RFC3339)); err != nil {
		return fmt.Errorf("sites: record note migration: %w", err)
	}
	return nil
}

// CleanNote trims a site description and rejects one longer than MaxNoteRunes.
func CleanNote(note string) (string, error) {
	note = strings.TrimSpace(note)
	note = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, note)
	if utf8.RuneCountInString(note) > MaxNoteRunes {
		return "", ErrNoteTooLong
	}
	return note, nil
}

// Create allocates a random slug owned by ownerID and makes its directory.
func (s *Store) Create(ctx context.Context, ownerID, note string) (Site, error) {
	if strings.TrimSpace(ownerID) == "" {
		return Site{}, errors.New("sites: missing owner")
	}
	note, err := CleanNote(note)
	if err != nil {
		return Site{}, err
	}
	var last error
	for range 8 {
		slug, err := RandomSlug()
		if err != nil {
			return Site{}, err
		}
		site := Site{
			ID:          newID(),
			Slug:        slug,
			OwnerUserID: ownerID,
			Note:        note,
			CreatedAt:   time.Now().UTC(),
		}
		_, err = s.db.ExecContext(ctx, `
			INSERT INTO sites (id, slug, owner_user_id, note, created_at) VALUES (?, ?, ?, ?, ?)`,
			site.ID, site.Slug, site.OwnerUserID, site.Note, formatTime(site.CreatedAt))
		if err == nil {
			if err := os.MkdirAll(s.siteDir(slug), 0o755); err != nil {
				return Site{}, err
			}
			return site, nil
		}
		if !isUnique(err) {
			return Site{}, err
		}
		last = err
	}
	return Site{}, fmt.Errorf("sites: allocate slug: %w", last)
}

// ListByOwner returns the caller's sites, newest first.
func (s *Store) ListByOwner(ctx context.Context, ownerID string) ([]Site, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, slug, owner_user_id, note, created_at FROM sites
		WHERE owner_user_id = ? ORDER BY created_at DESC, slug`, ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanSites(rows)
}

// GetBySlug loads a site by its public slug.
func (s *Store) GetBySlug(ctx context.Context, slug string) (Site, error) {
	if !ValidSlug(slug) {
		return Site{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, slug, owner_user_id, note, created_at FROM sites WHERE slug = ?`, slug)
	return scanSite(row)
}

// GetOwned loads a site only when ownerID owns it.
func (s *Store) GetOwned(ctx context.Context, ownerID, slug string) (Site, error) {
	if !ValidSlug(slug) {
		return Site{}, ErrNotFound
	}
	row := s.db.QueryRowContext(ctx, `
		SELECT id, slug, owner_user_id, note, created_at FROM sites
		WHERE slug = ? AND owner_user_id = ?`, slug, ownerID)
	return scanSite(row)
}

// SetNote replaces the description on a site the owner owns.
func (s *Store) SetNote(ctx context.Context, ownerID, slug, note string) (Site, error) {
	note, err := CleanNote(note)
	if err != nil {
		return Site{}, err
	}
	if !ValidSlug(slug) {
		return Site{}, ErrNotFound
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE sites SET note = ? WHERE slug = ? AND owner_user_id = ?`, note, slug, ownerID)
	if err != nil {
		return Site{}, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return Site{}, err
	}
	if n == 0 {
		return Site{}, ErrNotFound
	}
	return s.GetOwned(ctx, ownerID, slug)
}

// PutFile writes or replaces one file inside the site the owner owns.
func (s *Store) PutFile(ctx context.Context, ownerID, slug, rel string, r io.Reader) error {
	if _, err := s.GetOwned(ctx, ownerID, slug); err != nil {
		return err
	}
	full, err := s.resolve(slug, rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	// Re-check after creating parents so a symlink planted in between is rejected.
	full, err = s.resolve(slug, rel)
	if err != nil {
		return err
	}
	data, err := io.ReadAll(io.LimitReader(r, MaxFileBytes+1))
	if err != nil {
		return err
	}
	if len(data) > MaxFileBytes {
		return ErrTooLarge
	}
	tmp := full + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, full); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// DeleteFile removes one file. It does not remove directories.
func (s *Store) DeleteFile(ctx context.Context, ownerID, slug, rel string) error {
	if _, err := s.GetOwned(ctx, ownerID, slug); err != nil {
		return err
	}
	full, err := s.resolve(slug, rel)
	if err != nil {
		return err
	}
	info, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	if info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return ErrBadPath
	}
	if err := os.Remove(full); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return err
	}
	return nil
}

// ListFiles reads the site directory. Missing directories are an empty list.
func (s *Store) ListFiles(ctx context.Context, ownerID, slug string) ([]File, error) {
	if _, err := s.GetOwned(ctx, ownerID, slug); err != nil {
		return nil, err
	}
	root := s.siteDir(slug)
	var out []File
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) && path == root {
				return nil
			}
			return err
		}
		if d.IsDir() || d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, File{Path: filepath.ToSlash(rel), Size: info.Size()})
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		out = []File{}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// Serve writes the public files for slug. Unknown slugs are 404.
func (s *Store) Serve(w http.ResponseWriter, r *http.Request, slug string) {
	if _, err := s.GetBySlug(r.Context(), slug); err != nil {
		http.NotFound(w, r)
		return
	}
	root := s.siteDir(slug)
	if info, err := os.Stat(root); err != nil || !info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.FileServer(http.Dir(root)).ServeHTTP(w, r)
}

func (s *Store) siteDir(slug string) string {
	return filepath.Join(s.dataDir, "sites", slug)
}

// resolve returns an absolute file path inside the site root.
func (s *Store) resolve(slug, rel string) (string, error) {
	if !ValidSlug(slug) {
		return "", ErrNotFound
	}
	cleaned, err := CleanRelPath(rel)
	if err != nil {
		return "", err
	}
	root := s.siteDir(slug)
	cur := root
	for part := range strings.SplitSeq(cleaned, "/") {
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", ErrBadPath
		}
	}
	relBack, err := filepath.Rel(root, cur)
	if err != nil {
		return "", ErrBadPath
	}
	if relBack == ".." || strings.HasPrefix(relBack, ".."+string(os.PathSeparator)) {
		return "", ErrBadPath
	}
	return cur, nil
}

func scanSites(rows *sql.Rows) ([]Site, error) {
	var out []Site
	for rows.Next() {
		site, err := scanSite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, site)
	}
	if out == nil {
		out = []Site{}
	}
	return out, rows.Err()
}

type scanner interface {
	Scan(dest ...any) error
}

func scanSite(row scanner) (Site, error) {
	var site Site
	var created string
	if err := row.Scan(&site.ID, &site.Slug, &site.OwnerUserID, &site.Note, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Site{}, ErrNotFound
		}
		return Site{}, err
	}
	t, err := time.Parse(timeLayout, created)
	if err != nil {
		return Site{}, err
	}
	site.CreatedAt = t
	return site, nil
}

const timeLayout = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string {
	return t.UTC().Format(timeLayout)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE")
}

func hasColumn(ctx context.Context, db *sql.DB, table, column string) (bool, error) {
	rows, err := db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return false, fmt.Errorf("sites: table info: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}
