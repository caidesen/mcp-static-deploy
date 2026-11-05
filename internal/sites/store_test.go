package sites

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
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
	site, err := st.Create(ctx, "user-1")
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
