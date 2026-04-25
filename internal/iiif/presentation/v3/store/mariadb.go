package store

import (
	"context"
	"database/sql"
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
	for _, stmt := range schemaSQL {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("presentation mariadb schema: %w", err)
		}
	}
	return &MariaDBStore{db: db}, nil
}

func (s *MariaDBStore) Close() error {
	return s.db.Close()
}

func (s *MariaDBStore) GetManifest(ctx context.Context, itemID string) ([]byte, error) {
	return getJSON(ctx, s.db, `SELECT body FROM iiif_presentation_manifests WHERE item_id = ?`, itemID)
}

func (s *MariaDBStore) GetAnnotationPage(ctx context.Context, itemID, canvasID string) ([]byte, error) {
	return getJSON(ctx, s.db, `SELECT body FROM iiif_presentation_annotation_pages WHERE item_id = ? AND canvas_id = ?`, itemID, canvasID)
}

func (s *MariaDBStore) PutAnnotationPage(ctx context.Context, itemID, canvasID string, body []byte) error {
	_, err := s.db.ExecContext(ctx, `
INSERT INTO iiif_presentation_annotation_pages (item_id, canvas_id, body)
VALUES (?, ?, ?)
ON DUPLICATE KEY UPDATE body = VALUES(body), updated_at = CURRENT_TIMESTAMP(6)
`, itemID, canvasID, body)
	return err
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

var schemaSQL = []string{
	`CREATE TABLE IF NOT EXISTS iiif_presentation_manifests (
  item_id varchar(255) NOT NULL,
  body json NOT NULL,
  created_at timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (item_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,

	`CREATE TABLE IF NOT EXISTS iiif_presentation_annotation_pages (
  item_id varchar(255) NOT NULL,
  canvas_id varchar(255) NOT NULL,
  body json NOT NULL,
  created_at timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  updated_at timestamp(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
  PRIMARY KEY (item_id, canvas_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
}
