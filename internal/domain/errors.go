package domain

import "errors"

var (
	// ErrInsufficientFunds is returned when the source wallet does not have enough balance.
	ErrInsufficientFunds = errors.New("insufficient funds in source wallet")

	// ErrWalletNotFound is returned when a requested wallet does not exist.
	ErrWalletNotFound = errors.New("wallet not found")

	// ErrSameWalletTransfer is returned when source and destination wallets are identical.
	ErrSameWalletTransfer = errors.New("source and destination wallets cannot be the same")

	// ErrInvalidAmount is returned when the transfer amount is less than or equal to zero.
	ErrInvalidAmount = errors.New("transfer amount must be strictly positive")

	// ErrIdempotencyConflict is returned when an idempotency key is reused with differing parameters.
	ErrIdempotencyConflict = errors.New("idempotency key reused with different request payload")

	// ErrIdempotencyInProgress is returned when another request with the same idempotency key is currently executing.
	ErrIdempotencyInProgress = errors.New("request with this idempotency key is currently in progress")

	// ErrInvalidStateTransition is returned when an illegal transfer state transition is attempted.
	ErrInvalidStateTransition = errors.New("invalid transfer state transition")
)
