package domain

import (
	"time"
)

// Wallet represents an individual account holding funds.
type Wallet struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Balance   int64     `json:"balance"` // Stored in minor currency units (e.g. cents)
	Currency  string    `json:"currency"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// CanDebit returns true if the wallet has sufficient funds to cover the requested amount.
func (w *Wallet) CanDebit(amount int64) bool {
	return w.Balance >= amount
}

// Debit deducts funds from the wallet balance.
func (w *Wallet) Debit(amount int64) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	if !w.CanDebit(amount) {
		return ErrInsufficientFunds
	}
	w.Balance -= amount
	w.UpdatedAt = time.Now().UTC()
	return nil
}

// Credit adds funds to the wallet balance.
func (w *Wallet) Credit(amount int64) error {
	if amount <= 0 {
		return ErrInvalidAmount
	}
	w.Balance += amount
	w.UpdatedAt = time.Now().UTC()
	return nil
}
