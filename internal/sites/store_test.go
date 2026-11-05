package sites

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func TestPutFileRejectsTraversal(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "t.db"))+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	st := New(db, dir)
	site, err := st.Create(ctx, "user-1", "")
	if err != nil {
		t.Fatal(err)
	}
	err = st.PutFile(ctx, "user-1", site.Slug, "../outside.txt", bytes.NewReader([]byte("nope")))
	if err == nil {
		t.Fatal("traversal was accepted")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "outside.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("outside file exists: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "sites", "outside.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("sibling outside file exists: %v", statErr)
	}
	if err := st.PutFile(ctx, "user-1", site.Slug, "index.html", bytes.NewReader([]byte("hello"))); err != nil {
		t.Fatal(err)
	}
	files, err := st.ListFiles(ctx, "user-1", site.Slug)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != "index.html" || files[0].Size != 5 {
		t.Fatalf("files = %+v", files)
	}
	if _, err := st.GetOwned(ctx, "other", site.Slug); err != ErrNotFound {
		t.Fatalf("other owner got %v", err)
	}
}

func TestNoteRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(filepath.Join(dir, "t.db"))+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE sites (
		id TEXT PRIMARY KEY,
		slug TEXT NOT NULL UNIQUE,
		owner_user_id TEXT NOT NULL,
		created_at TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	st := New(db, dir)
	site, err := st.Create(ctx, "user-1", "  landing page  ")
	if err != nil {
		t.Fatal(err)
	}
	if site.Note != "landing page" {
		t.Fatalf("created note %q", site.Note)
	}
	updated, err := st.SetNote(ctx, "user-1", site.Slug, "docs")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Note != "docs" {
		t.Fatalf("updated note %q", updated.Note)
	}
	if _, err := st.SetNote(ctx, "user-1", site.Slug, strings.Repeat("x", MaxNoteRunes+1)); err != ErrNoteTooLong {
		t.Fatalf("long note: %v", err)
	}
	if _, err := st.SetNote(ctx, "other", site.Slug, "nope"); err != ErrNotFound {
		t.Fatalf("other owner set note: %v", err)
	}
}
