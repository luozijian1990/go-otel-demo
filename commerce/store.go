package commerce

import (
	"context"
	"errors"
	"fmt"
	"go-otel-demo/commerce/telemetry"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type Product struct {
	SKU   string `gorm:"primaryKey;size:40" json:"sku"`
	Name  string `json:"name"`
	Price int64  `json:"price_cents"`
}

func (Product) TableName() string { return "commerce_products" }

type Stock struct {
	SKU       string `gorm:"primaryKey;size:40" json:"sku"`
	Available int64  `json:"available"`
}

func (Stock) TableName() string { return "commerce_stock" }

type Reservation struct {
	OrderID  string `gorm:"primaryKey;size:64" json:"order_id"`
	SKU      string `json:"sku"`
	Quantity int64  `json:"quantity"`
	State    string `json:"state"`
}

func (Reservation) TableName() string { return "commerce_reservations" }

type Order struct {
	ID       string `gorm:"primaryKey;size:64" json:"id"`
	SKU      string `json:"sku"`
	Quantity int64  `json:"quantity"`
	Amount   int64  `json:"amount_cents"`
	State    string `json:"state"`
}

func (Order) TableName() string { return "commerce_orders" }

type Payment struct {
	OrderID string `gorm:"primaryKey;size:64" json:"order_id"`
	Amount  int64  `json:"amount_cents"`
	State   string `json:"state"`
}

func (Payment) TableName() string { return "commerce_payments" }

var errConflict = errors.New("business state conflict")
var errNoStock = errors.New("insufficient stock")

func (a *App) migrate() error {
	switch a.role {
	case "product-service":
		if err := a.db.AutoMigrate(&Product{}); err != nil {
			return err
		}
		return a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&Product{"SKU-001", "无线机械键盘", 19900}).Error
	case "inventory-service":
		if err := a.db.AutoMigrate(&Stock{}, &Reservation{}); err != nil {
			return err
		}
		return a.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&Stock{"SKU-001", 1000000}).Error
	case "payment-service":
		return a.db.AutoMigrate(&Payment{})
	case "order-service":
		return a.db.AutoMigrate(&Order{})
	}
	return fmt.Errorf("unknown service")
}
func (a *App) query(ctx context.Context, op string, fn func(*gorm.DB) error) error {
	start := time.Now()
	err := fn(a.db.WithContext(ctx))
	telemetry.Observe(ctx, "mysql", op, start, err)
	return err
}
func (a *App) reserve(ctx context.Context, r Reservation) error {
	return a.query(ctx, "reserve", func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var stock Stock
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&stock, "sku = ?", r.SKU).Error; err != nil {
				return err
			}
			var prior Reservation
			lookup := tx.Limit(1).Find(&prior, "order_id = ?", r.OrderID)
			if lookup.Error != nil {
				return lookup.Error
			}
			if lookup.RowsAffected == 1 {
				if prior.SKU != r.SKU || prior.Quantity != r.Quantity || prior.State == "released" {
					return errConflict
				}
				return nil
			}
			if stock.Available < r.Quantity {
				return errNoStock
			}
			if err := tx.Model(&stock).Update("available", stock.Available-r.Quantity).Error; err != nil {
				return err
			}
			r.State = "held"
			return tx.Create(&r).Error
		})
	})
}
func (a *App) transition(ctx context.Context, id, state string) error {
	return a.query(ctx, "reservation_update", func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			var r Reservation
			// Lock stock before reservation, same order as reserve, to avoid cross-operation deadlocks.
			if err := tx.First(&r, "order_id = ?", id).Error; err != nil {
				return err
			}
			var stock Stock
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&stock, "sku = ?", r.SKU).Error; err != nil {
				return err
			}
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&r, "order_id = ?", id).Error; err != nil {
				return err
			}
			if r.State == state {
				return nil
			}
			if r.State != "held" {
				return errConflict
			}
			if state == "released" {
				if err := tx.Model(&stock).Update("available", stock.Available+r.Quantity).Error; err != nil {
					return err
				}
			}
			return tx.Model(&r).Update("state", state).Error
		})
	})
}
