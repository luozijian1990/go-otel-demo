package database

import (
	"context"
	"fmt"
	"go-otel-demo/service-a/internal/telemetry"
	"strings"
	"time"

	"go-otel-demo/service-a/internal/model"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

type Store struct {
	db *gorm.DB
}

func New(ctx context.Context, dsn string) (*Store, error) {
	var db *gorm.DB
	var err error
	for attempt := 0; attempt < 5; attempt++ {
		db, err = gorm.Open(mysql.Open(dsn), &gorm.Config{})
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
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

func (s *Store) Ping(ctx context.Context) error {
	db, err := s.db.DB()
	if err != nil {
		return err
	}
	return db.PingContext(ctx)
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

func (s *Store) ListUsers(ctx context.Context) (_ []model.User, resultErr error) {
	start := time.Now()
	defer func() { telemetry.Observe(ctx, "mysql", "query", start, resultErr) }()
	var users []model.User
	if err := s.db.WithContext(ctx).Order("id asc").Find(&users).Error; err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return users, nil
}

func (s *Store) QueryBrokenTable(ctx context.Context) (resultErr error) {
	start := time.Now()
	defer func() { telemetry.Observe(ctx, "mysql", "query", start, resultErr) }()
	var rows []map[string]any
	if err := s.db.WithContext(ctx).Table("users_table_that_does_not_exist").Find(&rows).Error; err != nil {
		return fmt.Errorf("query broken table: %w", err)
	}
	return fmt.Errorf("expected broken table query to fail")
}
