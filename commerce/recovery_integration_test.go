package commerce

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Deliberately no configurable DSN: never run migrations against user dependencies.
func integrationDB(t *testing.T) *gorm.DB {
	t.Helper()
	if os.Getenv("COMMERCE_ISOLATED_MYSQL") != "1" {
		t.Skip("requires isolated MySQL on 127.0.0.1:23306, database commerce_fixes_test")
	}
	db, err := gorm.Open(mysql.Open("demo:isolated-test-only@tcp(127.0.0.1:23306)/commerce_fixes_test?parseTime=true&timeout=3s&readTimeout=5s&writeTimeout=5s"), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	db = db.WithContext(ctx)
	sql, _ := db.DB()
	t.Cleanup(func() { cancel(); sql.Close() })
	if err = db.AutoMigrate(&Stock{}, &Reservation{}, &Order{}, &Payment{}, &Product{}); err != nil {
		t.Fatal(err)
	}
	return db
}
func request(t *testing.T, h http.Handler, method, path string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	r := httptest.NewRequest(method, path, bytes.NewReader(b)).WithContext(ctx)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out map[string]any
	json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}
func router(a *App) *gin.Engine { r := gin.New(); a.routes(r); return r }
func TestReservationPersistence(t *testing.T) {
	db := integrationDB(t)
	sku := newID()
	db.Create(&Stock{SKU: sku, Available: 20})
	a := &App{role: "inventory-service", db: db}
	r := router(a)
	id := newID()
	body := Reservation{OrderID: id, SKU: sku, Quantity: 2}
	check := func(state string) {
		t.Helper()
		code, b := request(t, r, "POST", "/reservations", body)
		if code != 200 || b["state"] != state {
			t.Fatalf("want %s got %d %v", state, code, b)
		}
	}
	check("held")
	check("held")
	request(t, r, "POST", "/reservations/"+id+"/confirm", nil)
	check("confirmed")
	var stock Stock
	db.First(&stock, "sku = ?", sku)
	if stock.Available != 18 {
		t.Fatal(stock)
	}
	id2 := newID()
	body.OrderID = id2
	check("held")
	request(t, r, "POST", "/reservations/"+id2+"/release", nil)
	request(t, r, "POST", "/reservations/"+id2+"/release", nil)
	if status, _ := request(t, r, "POST", "/reservations", body); status != 409 {
		t.Fatal(status)
	}
	body.OrderID = newID()
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _ := request(t, r, "POST", "/reservations", body)
			if code != 200 {
				t.Errorf("concurrent status %d", code)
			}
		}()
	}
	wg.Wait()
	db.First(&stock, "sku = ?", sku)
	if stock.Available != 16 {
		t.Fatal(stock)
	}
}
func TestCommittedResponseLossRecovery(t *testing.T) {
	for _, phase := range []string{"reserve", "payment"} {
		for _, mode := range []string{"gateway_504", "broken_json", "truncated_body", "disconnect"} {
			t.Run(phase+"/"+mode, func(t *testing.T) {
				db := integrationDB(t)
				sku, id := newID(), newID()
				db.Create(&Stock{SKU: sku, Available: 20})
				db.Create(&Product{SKU: sku, Price: 100})
				inv := router(&App{role: "inventory-service", db: db})
				pay := router(&App{role: "payment-service", db: db})
				lost := false
				proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					rec := httptest.NewRecorder()
					if strings.HasPrefix(r.URL.Path, "/reservations") {
						inv.ServeHTTP(rec, r)
					} else {
						pay.ServeHTTP(rec, r)
					}
					if !lost && ((phase == "reserve" && r.URL.Path == "/reservations") || (phase == "payment" && r.URL.Path == "/payments")) {
						lost = true
						switch mode {
						case "gateway_504":
							w.WriteHeader(504)
						case "broken_json":
							w.Write([]byte(`{"state":`))
						case "truncated_body":
							w.Header().Set("Content-Length", "1000")
							w.Write([]byte(`{"state":"paid"}`))
						case "disconnect":
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
							} else {
								conn.Close()
							}
						}
						return
					}
					for k, v := range rec.Header() {
						w.Header()[k] = v
					}
					w.WriteHeader(rec.Code)
					w.Write(rec.Body.Bytes())
				}))
				defer proxy.Close()
				product := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(Product{SKU: sku, Price: 100}) }))
				defer product.Close()
				newApp := func() *App {
					return &App{role: "order-service", db: db, orderGate: make(chan struct{}, 1), peers: map[string]string{"product-service": product.URL, "inventory-service": proxy.URL, "payment-service": proxy.URL}}
				}
				r := router(newApp())
				body := map[string]any{"id": id, "sku": sku, "quantity": 2}
				status, _ := request(t, r, "POST", "/orders", body)
				if phase == "reserve" {
					if status != 500 {
						t.Fatal(status)
					}
					var o Order
					db.First(&o, "id = ?", id)
					if o.State != "reservation_unknown" {
						t.Fatalf("state=%s", o.State)
					}
					status, _ = request(t, router(newApp()), "POST", "/orders", body)
					if status != 200 {
						t.Fatal(status)
					}
				} else {
					if status != 201 {
						t.Fatal(status)
					}
					request(t, r, "POST", "/orders/"+id+"/pay", nil)
					var o Order
					db.First(&o, "id = ?", id)
					if o.State != "payment_unknown" {
						t.Fatalf("state=%s", o.State)
					}
					status, b := request(t, router(newApp()), "POST", "/orders/"+id+"/pay", nil)
					if status != 200 || b["state"] != "paid" {
						t.Fatalf("%d %v", status, b)
					}
					var n int64
					db.Model(&Payment{}).Where("order_id = ?", id).Count(&n)
					if n != 1 {
						t.Fatal(n)
					}
				}
				var stock Stock
				db.First(&stock, "sku = ?", sku)
				if stock.Available != 18 {
					t.Fatalf("stock=%d", stock.Available)
				}
			})
		}
	}
}

func TestPriorUnknownNeverCompensates(t *testing.T) {
	db := integrationDB(t)
	sku, id := newID(), newID()
	db.Create(&Stock{SKU: sku, Available: 18})
	db.Create(&Reservation{OrderID: id, SKU: sku, Quantity: 2, State: "held"})
	db.Create(&Order{ID: id, SKU: sku, Quantity: 2, Amount: 200, State: "payment_unknown"})
	inv := httptest.NewServer(router(&App{role: "inventory-service", db: db}))
	defer inv.Close()
	pay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		w.Write([]byte(`{"service":"payment-service","error":"internal_failure","code":"internal_failure","category":"internal_failure","operation":"payment","operation_outcome":"not_applied"}`))
	}))
	defer pay.Close()
	a := &App{role: "order-service", db: db, orderGate: make(chan struct{}, 1), peers: map[string]string{"inventory-service": inv.URL, "payment-service": pay.URL}}
	request(t, router(a), "POST", "/orders/"+id+"/pay", nil)
	var stock Stock
	db.First(&stock, "sku = ?", sku)
	var o Order
	db.First(&o, "id = ?", id)
	if stock.Available != 18 || o.State != "payment_unknown" {
		t.Fatalf("stock=%d state=%s", stock.Available, o.State)
	}
}

func TestPaymentFailureBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, initial, wire, want           string
		status                              int
		releaseFail, confirmFail, writeFail bool
	}{
		{name: "first_explicit_failure", initial: "awaiting_payment", status: 500, wire: `{"service":"payment-service","code":"internal_failure","category":"internal_failure","operation":"payment","operation_outcome":"not_applied"}`, want: "payment_failed"},
		{name: "raw_500", initial: "awaiting_payment", status: 500, wire: `oops`, want: "payment_unknown"},
		{name: "damaged_success", initial: "awaiting_payment", status: 200, wire: `{"state":`, want: "payment_unknown"},
		{name: "unknown_then_conflict", initial: "payment_unknown", status: 409, wire: `{"service":"payment-service","code":"state_conflict","category":"business_rejection","operation":"payment","operation_outcome":"not_applied"}`, want: "reconciliation_required"},
		{name: "release_failure", initial: "awaiting_payment", status: 500, wire: `{"service":"payment-service","code":"internal_failure","category":"internal_failure","operation":"payment","operation_outcome":"not_applied"}`, releaseFail: true, want: "reconciliation_required"},
		{name: "confirm_failure", initial: "awaiting_payment", status: 200, confirmFail: true, want: "reconciliation_required"},
		{name: "pending_write_failure", initial: "awaiting_payment", writeFail: true, want: "awaiting_payment"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := integrationDB(t)
			sku, id := newID(), newID()
			db.Create(&Stock{SKU: sku, Available: 18})
			db.Create(&Reservation{OrderID: id, SKU: sku, Quantity: 2, State: "held"})
			db.Create(&Order{ID: id, SKU: sku, Quantity: 2, Amount: 200, State: tc.initial})
			invRouter := router(&App{role: "inventory-service", db: db})
			inv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.releaseFail && strings.HasSuffix(r.URL.Path, "/release") || tc.confirmFail && strings.HasSuffix(r.URL.Path, "/confirm") {
					w.WriteHeader(504)
					return
				}
				invRouter.ServeHTTP(w, r)
			}))
			defer inv.Close()
			var payCalls atomic.Int64
			pay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				payCalls.Add(1)
				if tc.wire != "" {
					w.WriteHeader(tc.status)
					w.Write([]byte(tc.wire))
					return
				}
				router(&App{role: "payment-service", db: db}).ServeHTTP(w, r)
			}))
			defer pay.Close()
			if tc.writeFail {
				db.Callback().Update().Before("gorm:update").Register("test:fail", func(tx *gorm.DB) { tx.AddError(errors.New("test state storage failure")) })
				defer db.Callback().Update().Remove("test:fail")
			}
			a := &App{role: "order-service", db: db, orderGate: make(chan struct{}, 1), peers: map[string]string{"inventory-service": inv.URL, "payment-service": pay.URL}}
			code, _ := request(t, router(a), "POST", "/orders/"+id+"/pay", nil)
			if code < 400 {
				t.Fatal(code)
			}
			var o Order
			db.First(&o, "id = ?", id)
			if o.State != tc.want {
				t.Fatalf("state %s want %s", o.State, tc.want)
			}
			var stock Stock
			db.First(&stock, "sku = ?", sku)
			wantStock := int64(18)
			if tc.want == "payment_failed" {
				wantStock = 20
			}
			if stock.Available != wantStock {
				t.Fatal(stock)
			}
			if tc.writeFail && payCalls.Load() != 0 {
				t.Fatal("paid before pending persisted")
			}
			if tc.want == "payment_unknown" || tc.want == "reconciliation_required" {
				if status, _ := request(t, router(a), "POST", "/orders/"+id+"/cancel", nil); status != 409 {
					t.Fatal("unsafe cancellation", status)
				}
			}
		})
	}
}

func TestReservationRollbackConflictAndNoStock(t *testing.T) {
	db := integrationDB(t)
	sku := newID()
	db.Create(&Stock{SKU: sku, Available: 3})
	r := router(&App{role: "inventory-service", db: db})
	id := newID()
	body := Reservation{OrderID: id, SKU: sku, Quantity: 2}
	request(t, r, "POST", "/reservations", body)
	body.Quantity = 1
	if code, _ := request(t, r, "POST", "/reservations", body); code != 409 {
		t.Fatal(code)
	}
	body.OrderID = newID()
	body.Quantity = 2
	if code, b := request(t, r, "POST", "/reservations", body); code != 409 || b["code"] != "insufficient_stock" {
		t.Fatal(code, b)
	}
	db.Callback().Create().Before("gorm:create").Register("test:rollback", func(tx *gorm.DB) {
		if tx.Statement.Table == "commerce_reservations" {
			tx.AddError(errors.New("test insert failure"))
		}
	})
	defer db.Callback().Create().Remove("test:rollback")
	body.OrderID = newID()
	body.Quantity = 1
	if code, _ := request(t, r, "POST", "/reservations", body); code != 500 {
		t.Fatal(code)
	}
	var stock Stock
	db.First(&stock, "sku = ?", sku)
	if stock.Available != 1 {
		t.Fatal("rollback lost", stock)
	}
	var n int64
	db.Model(&Reservation{}).Where("order_id = ?", body.OrderID).Count(&n)
	if n != 0 {
		t.Fatal(n)
	}
}

func TestFailedStateWriteDoesNotClaimPersistence(t *testing.T) {
	db := integrationDB(t)
	o := Order{ID: newID(), SKU: newID(), Quantity: 1, Amount: 100, State: "payment_unknown"}
	db.Create(&o)
	// Failure after GORM has assigned update values, before executing SQL.
	db.Callback().Update().After("gorm:update").Before("gorm:commit_or_rollback_transaction").Register("test:fail-state", func(tx *gorm.DB) { tx.AddError(errors.New("storage unavailable")) })
	defer db.Callback().Update().Remove("test:fail-state")
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/orders/"+o.ID+"/pay", nil)
	if err := (&App{db: db}).setOrder(c, &o, "paid"); err == nil {
		t.Fatal("expected failure")
	}
	if o.State != "payment_unknown" {
		t.Fatal("unpersisted state claimed", o.State)
	}
}

func TestCancellationLostReleaseResponse(t *testing.T) {
	db := integrationDB(t)
	sku, id := newID(), newID()
	db.Create(&Stock{SKU: sku, Available: 18})
	db.Create(&Reservation{OrderID: id, SKU: sku, Quantity: 2, State: "held"})
	db.Create(&Order{ID: id, SKU: sku, Quantity: 2, Amount: 200, State: "awaiting_payment"})
	inv := router(&App{role: "inventory-service", db: db})
	var count atomic.Int64
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		inv.ServeHTTP(rec, r)
		if count.Add(1) == 1 {
			w.WriteHeader(504)
			return
		}
		w.WriteHeader(rec.Code)
		w.Write(rec.Body.Bytes())
	}))
	defer proxy.Close()
	a := &App{role: "order-service", db: db, orderGate: make(chan struct{}, 1), peers: map[string]string{"inventory-service": proxy.URL}}
	r := router(a)
	request(t, r, "POST", "/orders/"+id+"/cancel", nil)
	var o Order
	db.First(&o, "id = ?", id)
	if o.State != "cancel_pending" {
		t.Fatal(o)
	}
	if code, _ := request(t, r, "POST", "/orders/"+id+"/pay", nil); code != 409 {
		t.Fatal("payment allowed after release", code)
	}
	code, b := request(t, r, "POST", "/orders/"+id+"/cancel", nil)
	if code != 200 || b["state"] != "cancelled" {
		t.Fatal(code, b)
	}
	var stock Stock
	db.First(&stock, "sku = ?", sku)
	if stock.Available != 20 {
		t.Fatal(stock)
	}
}

func TestFinalizationAfterCancellationIsBounded(t *testing.T) {
	db := integrationDB(t)
	o := Order{ID: newID(), SKU: newID(), Quantity: 1, Amount: 100, State: "payment_unknown"}
	db.Create(&o)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/orders/"+o.ID+"/pay", nil).WithContext(ctx)
	start := time.Now()
	(&App{role: "order-service", db: db}).finishError(c, &o, "reconciliation_required", context.Canceled, nil)
	if time.Since(start) > 3*time.Second {
		t.Fatal("unbounded finalization")
	}
	var stored Order
	db.First(&stored, "id = ?", o.ID)
	if stored.State != "reconciliation_required" {
		t.Fatal(stored)
	}
}

func TestFallbackLookupFailureNeverLogsSuccess(t *testing.T) {
	db := integrationDB(t)
	db.Callback().Query().Before("gorm:query").Register("test:missing-products", func(tx *gorm.DB) { tx.Statement.Table = "isolated_nonexistent_products" })
	defer db.Callback().Query().Remove("test:missing-products")
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 50 * time.Millisecond})
	defer cache.Close()
	var output bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	defer slog.SetDefault(old)
	code, _ := request(t, router(&App{role: "product-service", db: db, cache: cache}), "GET", "/products/test", nil)
	if code != 500 || !strings.Contains(output.String(), "fallback_attempted") || !strings.Contains(output.String(), "fallback_failed") || strings.Contains(output.String(), "fallback_succeeded") || !strings.Contains(output.String(), "1146") {
		t.Fatal(code, output.String())
	}
}

func TestInsufficientStockThroughRealInventory(t *testing.T) {
	db := integrationDB(t)
	sku, id := newID(), newID()
	db.Create(&Stock{SKU: sku, Available: 0})
	inv := httptest.NewServer(router(&App{role: "inventory-service", db: db}))
	defer inv.Close()
	product := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { json.NewEncoder(w).Encode(Product{SKU: sku, Price: 100}) }))
	defer product.Close()
	a := &App{role: "order-service", db: db, orderGate: make(chan struct{}, 1), peers: map[string]string{"product-service": product.URL, "inventory-service": inv.URL}}
	code, b := request(t, router(a), "POST", "/orders", map[string]any{"id": id, "sku": sku, "quantity": 1})
	if code != 409 || b["code"] != "insufficient_stock" || b["upstream_service"] != "inventory-service" {
		t.Fatal(code, b)
	}
	var o Order
	db.First(&o, "id = ?", id)
	if o.State != "failed" {
		t.Fatal(o)
	}
}

func TestReservationRecoveryConflictRequiresReconciliation(t *testing.T) {
	db := integrationDB(t)
	sku, id := newID(), newID()
	db.Create(&Stock{SKU: sku, Available: 20})
	db.Create(&Reservation{OrderID: id, SKU: sku, Quantity: 2, State: "released"})
	db.Create(&Order{ID: id, SKU: sku, Quantity: 2, Amount: 200, State: "reservation_unknown"})
	inv := httptest.NewServer(router(&App{role: "inventory-service", db: db}))
	defer inv.Close()
	a := &App{role: "order-service", db: db, orderGate: make(chan struct{}, 1), peers: map[string]string{"inventory-service": inv.URL}}
	code, _ := request(t, router(a), "POST", "/orders", map[string]any{"id": id, "sku": sku, "quantity": 2})
	if code != 409 {
		t.Fatal(code)
	}
	var o Order
	db.First(&o, "id = ?", id)
	if o.State != "reconciliation_required" {
		t.Fatal(o)
	}
}
