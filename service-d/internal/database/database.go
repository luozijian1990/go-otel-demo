package database

import (
	"context"
	"fmt"
	"time"

	"go-otel-demo/service-d/internal/model"

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
		return fmt.Errorf("auto migrate users: %w", err)
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

func (s *Store) SlowListUsers(ctx context.Context, delay time.Duration) ([]model.User, error) {
	var ignored int
	if err := s.db.WithContext(ctx).Raw("SELECT SLEEP(?)", int(delay.Seconds())).Scan(&ignored).Error; err != nil {
		return nil, fmt.Errorf("mysql sleep: %w", err)
	}
	return s.ListUsers(ctx)
}
