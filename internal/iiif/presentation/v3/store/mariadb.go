package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"

	_ "github.com/go-sql-driver/mysql"
)

type MariaDBStore struct {
	db *sql.DB
}

func NewMariaDBStore(ctx context.Context, dsn string) (*MariaDBStore, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("presentation mariadb open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("presentation mariadb ping: %w", err)
	}
	return &MariaDBStore{db: db}, nil
}

// ApplyMariaDBSchema applies the Presentation MariaDB schema. Run this from a
// migration/deploy step with DDL privileges, not from normal server startup.
func ApplyMariaDBSchema(ctx context.Context, dsn string) error {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return fmt.Errorf("presentation mariadb open: %w", err)
	}
	defer db.Close()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("presentation mariadb ping: %w", err)
	}
	for _, stmt := range mariadbSchemaStatements {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("presentation mariadb schema: %w", err)
		}
	}
	return nil
}

func (s *MariaDBStore) Close() error {
	return s.db.Close()
}

func (s *MariaDBStore) GetManifest(ctx context.Context, itemID string) ([]byte, error) {
	return getJSON(ctx, s.db, selectManifestSQL, itemID)
}

func (s *MariaDBStore) GetAnnotationPage(ctx context.Context, itemID, canvasID string) ([]byte, error) {
	return getJSON(ctx, s.db, selectAnnotationPageSQL, itemID, canvasID)
}

func (s *MariaDBStore) PutAnnotationPage(ctx context.Context, itemID, canvasID string, body []byte, ifMatch string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var current []byte
	err = tx.QueryRowContext(ctx, selectAnnotationPageForUpdateSQL, itemID, canvasID).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		if ifMatch != "*" {
			return ErrPreconditionFailed
		}
		if _, err := tx.ExecContext(ctx, insertAnnotationPageSQL, itemID, canvasID, body); err != nil {
			return err
		}
		return tx.Commit()
	}
	if err != nil {
		return err
	}
	if ifMatch == "*" || !IfMatchMatches(ifMatch, DocumentETag(current)) {
		return ErrPreconditionFailed
	}
	if _, err := tx.ExecContext(ctx, updateAnnotationPageSQL, body, itemID, canvasID); err != nil {
		return err
	}
	return tx.Commit()
}

func getJSON(ctx context.Context, db *sql.DB, query string, args ...any) ([]byte, error) {
	var body []byte
	err := db.QueryRowContext(ctx, query, args...).Scan(&body)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return body, nil
}

//go:embed sql/schema/001_manifests.sql
var createManifestsTableSQL string

//go:embed sql/schema/002_annotation_pages.sql
var createAnnotationPagesTableSQL string

//go:embed sql/queries/select_manifest.sql
var selectManifestSQL string

//go:embed sql/queries/select_annotation_page.sql
var selectAnnotationPageSQL string

//go:embed sql/queries/insert_annotation_page.sql
var insertAnnotationPageSQL string

//go:embed sql/queries/update_annotation_page.sql
var updateAnnotationPageSQL string

//go:embed sql/queries/select_annotation_page_for_update.sql
var selectAnnotationPageForUpdateSQL string

var mariadbSchemaStatements = []string{
	createManifestsTableSQL,
	createAnnotationPagesTableSQL,
}
