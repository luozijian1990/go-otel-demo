package database

import (
	"context"
	"fmt"
	"strings"

	"go-otel-demo/service-a/internal/model"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type Store struct {
	db *gorm.DB
}

func New(ctx context.Context, dsn string) (*Store, error) {
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("connect mysql: %w", err)
	}

	store := &Store{db: db}
	if err := store.migrateAndSeed(ctx); err != nil {
		_ = store.Close()
		return nil, err
	}

	return store, nil
}

func (s *Store) Close() error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.Close()
}

func (s *Store) migrateAndSeed(ctx context.Context) error {
	if err := s.db.WithContext(ctx).AutoMigrate(&model.User{}); err != nil {
		if !isIgnorableEmailIndexMigrationError(err) {
			return fmt.Errorf("auto migrate users: %w", err)
		}
	}

	var count int64
	if err := s.db.WithContext(ctx).Model(&model.User{}).Count(&count).Error; err != nil {
		return fmt.Errorf("count users: %w", err)
	}
	if count > 0 {
		return nil
	}

	users := []model.User{
		{Name: "Ada Lovelace", Email: "ada@example.com"},
		{Name: "Grace Hopper", Email: "grace@example.com"},
		{Name: "Alan Turing", Email: "alan@example.com"},
	}
	if err := s.db.WithContext(ctx).Create(&users).Error; err != nil {
		return fmt.Errorf("seed users: %w", err)
	}
	return nil
}

func isIgnorableEmailIndexMigrationError(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "idx_users_email") &&
		(strings.Contains(msg, "Duplicate key name") || strings.Contains(msg, "Can't DROP"))
}

func (s *Store) ListUsers(ctx context.Context) ([]model.User, error) {
	var users []model.User
	if err := s.db.WithContext(ctx).Order("id asc").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (s *Store) QueryBrokenTable(ctx context.Context) error {
	var rows []map[string]any
	if err := s.db.WithContext(ctx).Table("users_table_that_does_not_exist").Find(&rows).Error; err != nil {
		return fmt.Errorf("query broken table: %w", err)
	}
	return fmt.Errorf("expected broken table query to fail")
}
