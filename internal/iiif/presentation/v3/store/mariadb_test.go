package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	_ "github.com/go-sql-driver/mysql"
)

func TestMariaDBSchemaHasNoForeignKeys(t *testing.T) {
	normalized := strings.ToLower(createResourcesTableSQL)
	for _, forbidden := range []string{"foreign key", "references"} {
		if strings.Contains(normalized, forbidden) {
			t.Fatalf("Presentation schema contains %q", forbidden)
		}
	}
}

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
	_, _ = db.ExecContext(ctx, `DELETE FROM iiif_presentation_resources WHERE resource_key LIKE 'triplet-test/%'`)

	key := "triplet-test/items/item-1/manifest"
	first := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/manifest","type":"Manifest","label":{"en":["Item 1"]},"items":[]}`)
	created, err := st.Put(ctx, key, first, Preconditions{IfNoneMatch: "*"})
	if err != nil || !created {
		t.Fatalf("create = %v, %v", created, err)
	}
	document, err := st.Get(ctx, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(document.Body) != string(first) || document.ModifiedAt.IsZero() {
		t.Fatalf("document = %#v", document)
	}
	second := []byte(`{"@context":"http://iiif.io/api/presentation/3/context.json","id":"https://example.org/manifest","type":"Manifest","label":{"en":["Item 1"]},"items":[],"summary":{"en":["updated"]}}`)
	created, err = st.Put(ctx, key, second, Preconditions{IfMatch: DocumentETag(first)})
	if err != nil || created {
		t.Fatalf("update = %v, %v", created, err)
	}
	if _, err := st.Put(ctx, key, first, Preconditions{IfMatch: DocumentETag(first)}); !errors.Is(err, ErrPreconditionFailed) {
		t.Fatalf("stale update err = %v", err)
	}
	if err := st.Delete(ctx, key, DocumentETag(second)); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := st.Get(ctx, key); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted err = %v", err)
	}

	concurrentKey := "triplet-test/concurrent-create"
	const attempts = 8
	var wait sync.WaitGroup
	results := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			_, err := st.Put(ctx, concurrentKey, first, Preconditions{IfNoneMatch: "*"})
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	succeeded := 0
	failed := 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if errors.Is(err, ErrPreconditionFailed) {
			failed++
		} else {
			t.Fatalf("concurrent create: %v", err)
		}
	}
	if succeeded != 1 || failed != attempts-1 {
		t.Fatalf("concurrent creates: succeeded=%d failed=%d", succeeded, failed)
	}
}
