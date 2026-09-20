package commerce

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go-otel-demo/commerce/telemetry"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

func bind(c *gin.Context, value any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 8192)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(value) != nil {
		c.AbortWithStatusJSON(400, gin.H{"error": "invalid JSON request"})
		return false
	}
	return true
}
func (a *App) routes(r *gin.Engine) {
	business := r.Group("", a.fault())
	switch a.role {
	case "product-service":
		business.GET("/products/:sku", a.product)
	case "inventory-service":
		business.GET("/inventory/:sku", a.inventory)
		business.POST("/reservations", a.reserveHandler)
		business.POST("/reservations/:id/release", a.reservationHandler("released"))
		business.POST("/reservations/:id/confirm", a.reservationHandler("confirmed"))
	case "payment-service":
		business.POST("/payments", a.charge)
	case "order-service":
		business.POST("/orders", a.createOrder)
		business.GET("/orders/:id", a.getOrder)
		business.POST("/orders/:id/pay", a.payOrder)
		business.POST("/orders/:id/cancel", a.cancelOrder)
	}
}
func (a *App) product(c *gin.Context) {
	ctx := c.Request.Context()
	sku := c.Param("sku")
	if !validID.MatchString(sku) {
		c.Status(400)
		return
	}
	cache := a.cache
	ctl, _ := c.Get("control")
	control, _ := ctl.(control)
	if control.Action == "cache_refused" && control.Target == a.role {
		cache = redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 100 * time.Millisecond, ReadTimeout: 100 * time.Millisecond, MaxRetries: -1, DisableIdentity: true, ContextTimeoutEnabled: true})
		defer cache.Close()
	}
	start := time.Now()
	cached, err := cache.Get(ctx, "commerce:product:"+sku).Result()
	telemetry.Observe(ctx, "redis", "get", start, func() error {
		if errors.Is(err, redis.Nil) {
			return nil
		}
		return err
	}())
	if control.Action == "cache_refused" {
		setReceipt(c, receipt{a.role, control.Action, true, err != nil && containsRefused(err)})
	}
	if err == nil {
		var p Product
		if json.Unmarshal([]byte(cached), &p) == nil {
			c.JSON(200, p)
			return
		}
	}
	fallback := err != nil && !errors.Is(err, redis.Nil)
	if fallback {
		telemetry.Fallback(ctx)
	}
	var p Product
	lookupErr := a.query(ctx, "product_lookup", func(db *gorm.DB) error { return db.First(&p, "sku = ?", sku).Error })
	if fallback {
		telemetry.FallbackResult(ctx, lookupErr)
	}
	if err := lookupErr; err != nil {
		a.fail(c, err)
		return
	}
	b, _ := json.Marshal(p)
	// Do not retry the failed cache as part of serving fallback.
	if err == nil || errors.Is(err, redis.Nil) {
		start = time.Now()
		e := a.cache.Set(ctx, "commerce:product:"+sku, b, time.Minute).Err()
		telemetry.Observe(ctx, "redis", "set", start, e)
	}
	c.JSON(200, p)
}
func containsRefused(err error) bool {
	return err != nil && strings.Contains(err.Error(), "connection refused")
}
func (a *App) inventory(c *gin.Context) {
	var stock Stock
	if err := a.query(c.Request.Context(), "stock_lookup", func(db *gorm.DB) error { return db.First(&stock, "sku = ?", c.Param("sku")).Error }); err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(200, stock)
}
func (a *App) reserveHandler(c *gin.Context) {
	var r Reservation
	if !bind(c, &r) {
		return
	}
	if !validID.MatchString(r.OrderID) || !validID.MatchString(r.SKU) || r.Quantity < 1 || r.Quantity > 5 || r.State != "" {
		c.Status(400)
		return
	}
	result, err := a.reserve(c.Request.Context(), r)
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(200, result)
}
func (a *App) reservationHandler(state string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !validID.MatchString(c.Param("id")) {
			c.Status(400)
			return
		}
		if err := a.transition(c.Request.Context(), c.Param("id"), state); err != nil {
			a.fail(c, err)
			return
		}
		telemetry.Log(c.Request.Context(), slog.LevelInfo, "reservation state changed", "order_id", c.Param("id"), "state", state)
		c.JSON(200, gin.H{"order_id": c.Param("id"), "state": state})
	}
}
func (a *App) charge(c *gin.Context) {
	var p Payment
	if !bind(c, &p) {
		return
	}
	if !validID.MatchString(p.OrderID) || p.Amount <= 0 || p.Amount > 10000000 || p.State != "" {
		c.Status(400)
		return
	}
	p.State = "paid"
	err := a.query(c.Request.Context(), "payment_record", func(db *gorm.DB) error {
		return db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&p).Error; err != nil {
				return err
			}
			var prior Payment
			if err := tx.First(&prior, "order_id = ?", p.OrderID).Error; err != nil {
				return err
			}
			if prior.Amount != p.Amount {
				return errConflict
			}
			return nil
		})
	})
	if err != nil {
		a.fail(c, err)
		return
	}
	telemetry.Log(c.Request.Context(), slog.LevelInfo, "payment recorded", "order_id", p.OrderID, "state", "paid")
	c.JSON(200, p)
}
func (a *App) lock(c *gin.Context) bool {
	select {
	case a.orderGate <- struct{}{}:
		return true
	case <-c.Request.Context().Done():
		a.fail(c, c.Request.Context().Err())
		return false
	}
}
func (a *App) createOrder(c *gin.Context) {
	var req struct {
		ID       string `json:"id"`
		SKU      string `json:"sku"`
		Quantity int64  `json:"quantity"`
	}
	if !bind(c, &req) {
		return
	}
	if !validID.MatchString(req.ID) || !validID.MatchString(req.SKU) || req.Quantity < 1 || req.Quantity > 5 {
		c.Status(400)
		return
	}
	if !a.lock(c) {
		return
	}
	defer func() { <-a.orderGate }()
	var prior Order
	err := a.query(c.Request.Context(), "order_lookup", func(db *gorm.DB) error { return db.Limit(1).Find(&prior, "id = ?", req.ID).Error })
	if err != nil {
		a.fail(c, err)
		return
	}
	recovering := prior.ID != ""
	o := prior
	if recovering {
		if prior.SKU != req.SKU || prior.Quantity != req.Quantity {
			a.fail(c, errConflict)
			return
		}
		if prior.State != "creating" && prior.State != "reservation_unknown" {
			c.JSON(200, prior)
			return
		}
	} else {
		var p Product
		if err := a.call(c, "product-service", "GET", "/products/"+req.SKU, nil, &p); err != nil {
			a.fail(c, err)
			return
		}
		o = Order{req.ID, req.SKU, req.Quantity, p.Price * req.Quantity, "creating"}
		if err := a.query(c.Request.Context(), "order_create", func(db *gorm.DB) error { return db.Create(&o).Error }); err != nil {
			a.fail(c, err)
			return
		}
	}
	var reservation Reservation
	err = a.call(c, "inventory-service", "POST", "/reservations", Reservation{OrderID: o.ID, SKU: o.SKU, Quantity: o.Quantity}, &reservation)
	if err != nil {
		state := "reservation_unknown"
		if recovering && reconciliationConflict(err) {
			state = "reconciliation_required"
		}
		if !recovering && definitelyNotApplied(err) {
			state = "failed"
		}
		a.finishError(c, &o, state, err, nil)
		return
	}
	if reservation.State != "held" {
		a.finishError(c, &o, "reconciliation_required", errConflict, nil)
		return
	}
	// Keep creating/unknown as a retry entry if the final state write fails. No
	// release is attempted here: its lost response could leave a payable order.
	if err := a.setOrder(c, &o, "awaiting_payment"); err != nil {
		a.finishError(c, &o, "reservation_unknown", err, nil)
		return
	}
	status := 201
	if recovering {
		status = 200
	}
	c.JSON(status, o)
}

// A hard-bounded final state write preserves correlation after request cancellation.
// Responses report only the last acknowledged persistent state.
func (a *App) finishError(c *gin.Context, o *Order, state string, primary, compensation error) {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(c.Request.Context()), 2*time.Second)
	defer cancel()
	cleanup := c.Copy()
	cleanup.Request = c.Request.Clone(ctx)
	persistErr := a.setOrder(cleanup, o, state)
	compensationOutcome := "not_attempted"
	if compensation != nil {
		compensationOutcome = classify(compensation, "release").Outcome
	}
	if c.GetBool("compensation_applied") {
		compensationOutcome = "applied"
	}
	needsRecovery := persistErr != nil || state == "payment_unknown" || state == "reservation_unknown" || state == "cancel_pending" || state == "reconciliation_required"
	attrs := []any{"order_id", o.ID, "state", o.State, "requested_state", state, "state_persisted", persistErr == nil, "reconciliation_required", needsRecovery, "primary_error", primary.Error(), "operation_outcome", classify(primary, operation(c.Request.URL.Path)).Outcome, "compensation_outcome", compensationOutcome}
	if compensation != nil {
		attrs = append(attrs, "compensation_error", compensation.Error())
	}
	if persistErr != nil {
		attrs = append(attrs, "state_error", persistErr.Error())
	}
	level := slog.LevelError
	if classify(primary, operation(c.Request.URL.Path)).BusinessRejected() && compensation == nil && persistErr == nil {
		level = slog.LevelWarn
	}
	telemetry.Log(ctx, level, "order operation outcome", attrs...)
	c.Set("order_state", o.State)
	c.Set("state_persisted", persistErr == nil)
	err := primary
	if compensation != nil || persistErr != nil {
		err = &OperationError{Code: "internal_failure", Category: "internal_failure", Operation: operation(c.Request.URL.Path), Outcome: "unknown", Cause: errors.Join(primary, compensation, persistErr)}
	}
	a.fail(c, err)
}

func (a *App) setOrder(c *gin.Context, o *Order, state string) error {
	err := a.query(c.Request.Context(), "order_update", func(db *gorm.DB) error {
		result := db.Model(&Order{}).Where("id = ?", o.ID).Update("state", state)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			var persisted Order
			if err := db.First(&persisted, "id = ?", o.ID).Error; err != nil {
				return err
			}
			if persisted.State != state {
				return errConflict
			}
		}
		return nil
	})
	if err == nil {
		o.State = state
		telemetry.Log(c.Request.Context(), slog.LevelInfo, "order state changed", "order_id", o.ID, "state", state)
	} else {
		telemetry.Log(c.Request.Context(), slog.LevelError, "order state persistence failed", "order_id", o.ID, "state", o.State, "requested_state", state, "state_persisted", false, "error_message", err.Error())
	}
	c.Set("order_state", o.State)
	c.Set("state_persisted", err == nil)
	return err
}
func (a *App) loadOrder(c *gin.Context) (Order, error) {
	var o Order
	err := a.query(c.Request.Context(), "order_lookup", func(db *gorm.DB) error { return db.First(&o, "id = ?", c.Param("id")).Error })
	return o, err
}
func (a *App) getOrder(c *gin.Context) {
	o, err := a.loadOrder(c)
	if err != nil {
		a.fail(c, err)
		return
	}
	c.JSON(200, o)
}
func (a *App) payOrder(c *gin.Context) {
	if !a.lock(c) {
		return
	}
	defer func() { <-a.orderGate }()
	o, err := a.loadOrder(c)
	if err != nil {
		a.fail(c, err)
		return
	}
	if o.State == "paid" {
		c.JSON(200, o)
		return
	}
	if o.State != "awaiting_payment" && o.State != "payment_unknown" {
		a.fail(c, errConflict)
		return
	}
	recovering := o.State == "payment_unknown"
	if err := a.setOrder(c, &o, "payment_unknown"); err != nil {
		a.fail(c, err)
		return
	}
	var p Payment
	err = a.call(c, "payment-service", "POST", "/payments", Payment{OrderID: o.ID, Amount: o.Amount}, &p)
	if err != nil {
		if !recovering && definitelyNotApplied(err) {
			// Persist the compensation intent before release, so a crash cannot resume payment.
			if stateErr := a.setOrder(c, &o, "reconciliation_required"); stateErr != nil {
				a.finishError(c, &o, "reconciliation_required", err, stateErr)
				return
			}
			if releaseErr := a.call(c, "inventory-service", "POST", "/reservations/"+o.ID+"/release", nil, nil); releaseErr != nil {
				a.finishError(c, &o, "reconciliation_required", err, releaseErr)
				return
			}
			c.Set("compensation_applied", true)
			telemetry.Log(c.Request.Context(), slog.LevelInfo, "payment compensation completed", "order_id", o.ID, "compensation_outcome", "applied")
			a.finishError(c, &o, "payment_failed", err, nil)
			return
		}
		state := "payment_unknown"
		if reconciliationConflict(err) {
			state = "reconciliation_required"
		}
		a.finishError(c, &o, state, err, nil)
		return
	}
	if err := a.call(c, "inventory-service", "POST", "/reservations/"+o.ID+"/confirm", nil, nil); err != nil {
		a.finishError(c, &o, "reconciliation_required", err, nil)
		return
	}
	if err := a.setOrder(c, &o, "paid"); err != nil {
		a.finishError(c, &o, "payment_unknown", err, nil)
		return
	}
	c.JSON(200, o)
}
func (a *App) cancelOrder(c *gin.Context) {
	if !a.lock(c) {
		return
	}
	defer func() { <-a.orderGate }()
	o, err := a.loadOrder(c)
	if err != nil {
		a.fail(c, err)
		return
	}
	if o.State == "cancelled" {
		c.JSON(200, o)
		return
	}
	if o.State != "awaiting_payment" && o.State != "cancel_pending" {
		a.fail(c, errConflict)
		return
	}
	if err := a.setOrder(c, &o, "cancel_pending"); err != nil {
		a.fail(c, err)
		return
	}
	if err := a.call(c, "inventory-service", "POST", "/reservations/"+o.ID+"/release", nil, nil); err != nil {
		a.finishError(c, &o, "cancel_pending", err, nil)
		return
	}
	if err := a.setOrder(c, &o, "cancelled"); err != nil {
		a.finishError(c, &o, "cancel_pending", err, nil)
		return
	}
	c.JSON(200, o)
}
