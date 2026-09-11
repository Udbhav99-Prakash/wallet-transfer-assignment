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

	// ErrMissingIdempotencyKey is returned when the idempotency key is empty.
	ErrMissingIdempotencyKey = errors.New("idempotency key is required")

	// ErrMissingWalletID is returned when the source or destination wallet ID is empty.
	ErrMissingWalletID = errors.New("source and destination wallet IDs are required")

	// ErrWalletNameRequired is returned when the wallet name is empty.
	ErrWalletNameRequired = errors.New("wallet name is required")

	// ErrNegativeBalance is returned when a negative initial balance is provided.
	ErrNegativeBalance = errors.New("initial balance cannot be negative")

	// ErrBalanceOverflow is returned when crediting an amount would overflow the maximum wallet balance.
	ErrBalanceOverflow = errors.New("balance overflow: maximum wallet balance exceeded")

	// ErrCurrencyMismatch is returned when source and destination wallets have different currencies.
	ErrCurrencyMismatch = errors.New("source and destination wallets have different currencies")

	// ErrTransferNotFound is returned when a requested transfer does not exist.
	ErrTransferNotFound = errors.New("transfer not found")

	// ErrWalletAlreadyExists is returned when attempting to create a wallet with an existing ID.
	ErrWalletAlreadyExists = errors.New("wallet already exists")

	// ErrInvalidIdempotencyKey is returned when the idempotency key is invalid or exceeds maximum length.
	ErrInvalidIdempotencyKey = errors.New("idempotency key must not be empty and must not exceed 128 characters")

	// ErrInvalidLedgerPair is returned when ledger entries do not form a balanced double-entry pair.
	ErrInvalidLedgerPair = errors.New("ledger entries must contain exactly one matching DEBIT and CREDIT pair")

	// ErrIdempotencyLeaseLost is returned when a worker attempts to finalize or cleanup an idempotency reservation whose lease was lost.
	ErrIdempotencyLeaseLost = errors.New("idempotency reservation lease was lost to a concurrent worker")
)
