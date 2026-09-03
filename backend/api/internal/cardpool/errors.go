package cardpool

import (
	"errors"
	"fmt"
)

var (
	ErrUnsupportedCapability   = errors.New("unsupported provider capability")
	ErrProviderNotFound        = errors.New("card provider not found")
	ErrNoAvailableCard         = errors.New("no available card")
	ErrAllocationInProgress    = errors.New("card allocation is already in progress")
	ErrAllocationRequired      = errors.New("card allocation id is required")
	ErrAllocationNotFound      = errors.New("card allocation not found")
	ErrAllocationClosed        = errors.New("card allocation is already closed")
	ErrCardNotFound            = errors.New("payment card not found")
	ErrCardConsumed            = errors.New("payment card has already been consumed")
	ErrCardUnavailable         = errors.New("payment card is unavailable for allocation")
	ErrCardPoolMismatch        = errors.New("payment card belongs to another pool")
	ErrProvisionInProgress     = errors.New("card provision is already in progress")
	ErrProvisionClosed         = errors.New("card provision is already closed")
	ErrInvalidWebhook          = errors.New("invalid provider webhook")
	ErrInvalidWebhookSignature = errors.New("invalid provider webhook signature")
)

type ErrorCategory string

const (
	CategoryTechnicalFailure    ErrorCategory = "TECHNICAL_FAILURE"
	CategoryBusinessDecline     ErrorCategory = "BUSINESS_DECLINE"
	CategoryInsufficientFunds   ErrorCategory = "INSUFFICIENT_FUNDS"
	CategoryComplianceBlock     ErrorCategory = "COMPLIANCE_BLOCK"
	CategoryProviderUnavailable ErrorCategory = "PROVIDER_UNAVAILABLE"
	CategoryRateLimit           ErrorCategory = "RATE_LIMIT"
	CategoryInvalidRequest      ErrorCategory = "INVALID_REQUEST"
	CategoryNotFound            ErrorCategory = "NOT_FOUND"
)

type ProviderError struct {
	Provider        string
	Operation       string
	Category        ErrorCategory
	Retryable       bool
	FailoverAllowed bool
	Err             error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "provider error"
	}
	message := "provider error"
	if e.Err != nil {
		message = e.Err.Error()
	}
	if e.Provider == "" && e.Operation == "" {
		return message
	}
	return fmt.Sprintf("%s %s: %s", e.Provider, e.Operation, message)
}

func (e *ProviderError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewProviderError(provider, operation string, category ErrorCategory, retryable, failoverAllowed bool, err error) error {
	return &ProviderError{Provider: provider, Operation: operation, Category: category, Retryable: retryable, FailoverAllowed: failoverAllowed, Err: err}
}

func UnsupportedCapability(provider string, capability Capability) error {
	return &ProviderError{Provider: provider, Operation: string(capability), Category: CategoryInvalidRequest, Err: fmt.Errorf("%w: %s", ErrUnsupportedCapability, capability)}
}

func IsUnsupportedCapability(err error) bool {
	return errors.Is(err, ErrUnsupportedCapability)
}

func IsFailoverEligible(err error) bool {
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) || providerErr == nil || !providerErr.FailoverAllowed {
		return false
	}
	switch providerErr.Category {
	case CategoryTechnicalFailure, CategoryProviderUnavailable:
		return true
	default:
		return false
	}
}

func IsWebhookValidationError(err error) bool {
	return errors.Is(err, ErrInvalidWebhook) || errors.Is(err, ErrInvalidWebhookSignature)
}
