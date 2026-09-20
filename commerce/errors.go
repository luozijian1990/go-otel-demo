package commerce

import (
	"errors"
	"gorm.io/gorm"
	"strings"
)

// Outcome describes this operation attempt, never all attempts for an idempotency key.
type OperationError struct {
	Code      string `json:"code"`
	Category  string `json:"category"`
	Operation string `json:"operation"`
	Outcome   string `json:"operation_outcome"`
	Upstream  string `json:"upstream_service,omitempty"`
	Cause     error  `json:"-"`
}

func (e *OperationError) Error() string {
	if e.Cause != nil {
		return e.Cause.Error()
	}
	return e.Code
}
func (e *OperationError) Unwrap() error          { return e.Cause }
func (e *OperationError) BusinessRejected() bool { return e.Category == "business_rejection" }
func (e *OperationError) Status() int {
	if e.BusinessRejected() {
		if e.Code == "resource_not_found" {
			return 404
		}
		return 409
	}
	return 500
}
func classify(err error, op string) *OperationError {
	var e *OperationError
	if errors.As(err, &e) {
		v := *e
		return &v
	}
	code, category, outcome := "internal_failure", "internal_failure", "unknown"
	switch {
	case errors.Is(err, errNoStock):
		code, category, outcome = "insufficient_stock", "business_rejection", "not_applied"
	case errors.Is(err, errConflict):
		code, category, outcome = "state_conflict", "business_rejection", "not_applied"
	case errors.Is(err, gorm.ErrRecordNotFound):
		code, category, outcome = "resource_not_found", "business_rejection", "not_applied"
	}
	return &OperationError{Code: code, Category: category, Operation: op, Outcome: outcome, Cause: err}
}
func operation(path string) string {
	switch {
	case strings.HasPrefix(path, "/products/"):
		return "product_lookup"
	case strings.HasPrefix(path, "/inventory/"):
		return "stock_lookup"
	case strings.HasSuffix(path, "/release"):
		return "release"
	case strings.HasSuffix(path, "/confirm"):
		return "confirm"
	case path == "/reservations":
		return "reserve"
	case path == "/payments":
		return "payment"
	case strings.HasSuffix(path, "/pay"):
		return "pay_order"
	case strings.HasSuffix(path, "/cancel"):
		return "cancel_order"
	case path == "/orders":
		return "create_order"
	}
	return "order_lookup"
}
func validContract(e *OperationError, status int, op string) bool {
	if e.Operation != op || e.Outcome != "not_applied" && e.Outcome != "unknown" {
		return false
	}
	if e.Category == "business_rejection" {
		switch e.Code {
		case "insufficient_stock", "state_conflict", "idempotency_conflict", "resource_not_found":
			return e.Outcome == "not_applied" && status == e.Status()
		}
	}
	return status >= 500 && status <= 599 && (e.Category == "internal_failure" && e.Code == "internal_failure" || e.Category == "dependency_failure" && (e.Code == "dependency_failure" || e.Code == "dependency_timeout" || e.Code == "invalid_dependency_response"))
}
func definitelyNotApplied(err error) bool {
	var e *OperationError
	return errors.As(err, &e) && e.Outcome == "not_applied" && e.Code != "state_conflict" && e.Code != "idempotency_conflict"
}

func reconciliationConflict(err error) bool {
	var e *OperationError
	return errors.As(err, &e) && e.BusinessRejected() && (e.Code == "state_conflict" || e.Code == "idempotency_conflict")
}
