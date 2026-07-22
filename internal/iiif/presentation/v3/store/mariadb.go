package store

import (
	"context"
	"database/sql"
	_ "embed"
	"errors"
	"fmt"
	"time"

	mysql "github.com/go-sql-driver/mysql"
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

// Get implements Store.
func (s *MariaDBStore) Get(ctx context.Context, resourceKey string) (Document, error) {
	if !validResourceKey(resourceKey) {
		return Document{}, ErrNotFound
	}
	var body []byte
	var modifiedAt time.Time
	err := s.db.QueryRowContext(ctx, selectResourceSQL, resourceKey).Scan(&body, &modifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Document{}, ErrNotFound
	}
	if err != nil {
		return Document{}, err
	}
	return Document{Body: body, ModifiedAt: modifiedAt.UTC()}, nil
}

// Put implements Store.
func (s *MariaDBStore) Put(ctx context.Context, resourceKey string, body []byte, conditions Preconditions) (bool, error) {
	if !validResourceKey(resourceKey) {
		return false, ErrNotFound
	}
	if conditions.IfNoneMatch != "" {
		if !putPreconditionMatches(false, "", conditions) {
			return false, ErrPreconditionFailed
		}
		if _, err := s.db.ExecContext(ctx, insertResourceSQL, resourceKey, body); err != nil {
			if isDuplicateKey(err) {
				return false, ErrPreconditionFailed
			}
			return false, err
		}
		return true, nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var current []byte
	err = tx.QueryRowContext(ctx, selectResourceForUpdateSQL, resourceKey).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return false, ErrPreconditionFailed
	}
	if err != nil {
		return false, err
	}
	if !putPreconditionMatches(true, DocumentETag(current), conditions) {
		return false, ErrPreconditionFailed
	}
	if _, err := tx.ExecContext(ctx, updateResourceSQL, body, resourceKey); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return false, nil
}

// Delete implements Store.
func (s *MariaDBStore) Delete(ctx context.Context, resourceKey, ifMatch string) error {
	if !validResourceKey(resourceKey) {
		return ErrNotFound
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()
	var current []byte
	err = tx.QueryRowContext(ctx, selectResourceForUpdateSQL, resourceKey).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrPreconditionFailed
	}
	if err != nil {
		return err
	}
	if !IfMatchMatches(ifMatch, DocumentETag(current)) {
		return ErrPreconditionFailed
	}
	if _, err := tx.ExecContext(ctx, deleteResourceSQL, resourceKey); err != nil {
		return err
	}
	return tx.Commit()
}

func isDuplicateKey(err error) bool {
	var mysqlErr *mysql.MySQLError
	return errors.As(err, &mysqlErr) && mysqlErr.Number == 1062
}

//go:embed sql/schema/001_resources.sql
var createResourcesTableSQL string

//go:embed sql/queries/select_resource.sql
var selectResourceSQL string

//go:embed sql/queries/select_resource_for_update.sql
var selectResourceForUpdateSQL string

//go:embed sql/queries/insert_resource.sql
var insertResourceSQL string

//go:embed sql/queries/update_resource.sql
var updateResourceSQL string

//go:embed sql/queries/delete_resource.sql
var deleteResourceSQL string

var mariadbSchemaStatements = []string{createResourcesTableSQL}
