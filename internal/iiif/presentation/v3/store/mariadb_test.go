package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

func TestMariaDBStoreIntegration(t *testing.T) {
	dsn := os.Getenv("TEST_DSN")
	if dsn == "" {
		t.Skip("TEST_DSN not set")
	}
	ctx := context.Background()
	if err := ApplyMariaDBSchema(ctx, dsn); err != nil {
		t.Fatal(err)
	}
	st, err := NewMariaDBStore(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, _ = db.ExecContext(ctx, `DELETE FROM iiif_presentation_annotation_pages WHERE item_id = 'item-1'`)
	_, _ = db.ExecContext(ctx, `DELETE FROM iiif_presentation_manifests WHERE item_id = 'item-1'`)

	manifest := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"http://example.test/presentation/v3/item-1/manifest","type":"Manifest","label":{"en":["Item 1"]},"items":[]}`)
	if _, err := db.ExecContext(ctx, `INSERT INTO iiif_presentation_manifests (item_id, body) VALUES (?, ?)`, "item-1", manifest); err != nil {
		t.Fatal(err)
	}
	got, err := st.GetManifest(ctx, "item-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(manifest) {
		t.Fatalf("manifest = %q", string(got))
	}

	page := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"http://example.test/presentation/v3/item-1/canvas/canvas-1/annotations","type":"AnnotationPage","items":[]}`)
	if err := st.PutAnnotationPage(ctx, "item-1", "canvas-1", page, "*"); err != nil {
		t.Fatal(err)
	}
	got, err = st.GetAnnotationPage(ctx, "item-1", "canvas-1")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(page) {
		t.Fatalf("page = %q", string(got))
	}

	_, err = st.GetManifest(ctx, "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
}
