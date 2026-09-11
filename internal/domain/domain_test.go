package domain_test

import (
	"testing"

	"wallet-transfer-assignment/internal/domain"
)

func TestWallet_DebitAndCredit(t *testing.T) {
	w := &domain.Wallet{
		ID:      "w1",
		Balance: 100,
	}

	// Test successful debit
	if err := w.Debit(40); err != nil {
		t.Fatalf("unexpected debit error: %v", err)
	}
	if w.Balance != 60 {
		t.Fatalf("expected balance 60, got %d", w.Balance)
	}

	// Test debiting more than balance
	if err := w.Debit(70); err != domain.ErrInsufficientFunds {
		t.Fatalf("expected ErrInsufficientFunds, got %v", err)
	}

	// Test negative debit amount
	if err := w.Debit(-10); err != domain.ErrInvalidAmount {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}

	// Test credit
	if err := w.Credit(50); err != nil {
		t.Fatalf("unexpected credit error: %v", err)
	}
	if w.Balance != 110 {
		t.Fatalf("expected balance 110, got %d", w.Balance)
	}
}

func TestTransfer_Transitions(t *testing.T) {
	tr := &domain.Transfer{
		ID:             "t1",
		IdempotencyKey: "k1",
		FromWalletID:   "w1",
		ToWalletID:     "w2",
		Amount:         100,
		Status:         domain.TransferStatusPending,
	}

	if err := tr.Validate(); err != nil {
		t.Fatalf("validation failed: %v", err)
	}

	// Transition to processed
	if err := tr.MarkProcessed(); err != nil {
		t.Fatalf("failed to mark processed: %v", err)
	}
	if tr.Status != domain.TransferStatusProcessed {
		t.Fatalf("expected status PROCESSED, got %s", tr.Status)
	}

	// Re-transitioning should fail
	if err := tr.MarkProcessed(); err != domain.ErrInvalidStateTransition {
		t.Fatalf("expected ErrInvalidStateTransition, got %v", err)
	}
}

func TestTransfer_ValidationErrors(t *testing.T) {
	// Same wallet
	tr := &domain.Transfer{
		ID:             "t1",
		IdempotencyKey: "k1",
		FromWalletID:   "w1",
		ToWalletID:     "w1",
		Amount:         100,
	}
	if err := tr.Validate(); err != domain.ErrSameWalletTransfer {
		t.Fatalf("expected ErrSameWalletTransfer, got %v", err)
	}

	// Zero amount
	tr.ToWalletID = "w2"
	tr.Amount = 0
	if err := tr.Validate(); err != domain.ErrInvalidAmount {
		t.Fatalf("expected ErrInvalidAmount, got %v", err)
	}
}

func TestLedger_NewDoubleEntryPair(t *testing.T) {
	debit, credit, err := domain.NewDoubleEntryPair("t1", "w1", "w2", 500)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if debit.Type != domain.LedgerEntryTypeDebit || debit.WalletID != "w1" || debit.Amount != 500 {
		t.Fatalf("invalid debit entry: %+v", debit)
	}
	if credit.Type != domain.LedgerEntryTypeCredit || credit.WalletID != "w2" || credit.Amount != 500 {
		t.Fatalf("invalid credit entry: %+v", credit)
	}
}

func TestIdempotency_ComputeRequestHash(t *testing.T) {
	h1 := domain.ComputeRequestHash("w1", "w2", 100)
	h2 := domain.ComputeRequestHash("w1", "w2", 100)
	h3 := domain.ComputeRequestHash("w1", "w2", 200)

	if h1 != h2 {
		t.Fatalf("expected hashes to match for identical inputs")
	}
	if h1 == h3 {
		t.Fatalf("expected different hashes for different amounts")
	}
}
