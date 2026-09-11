package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/repository"
)

// WalletService provides wallet inspection, creation, and ledger balance reconciliation.
type WalletService struct {
	repos repository.Repositories
}

// NewWalletService constructs a WalletService.
func NewWalletService(repos repository.Repositories) *WalletService {
	return &WalletService{repos: repos}
}

// CreateWallet creates a new wallet with an initial balance and initial credit ledger record if balance > 0.
func (s *WalletService) CreateWallet(ctx context.Context, req CreateWalletRequest) (*domain.Wallet, error) {
	if req.Name == "" {
		return nil, errors.New("wallet name is required")
	}
	if req.InitialBalance < 0 {
		return nil, errors.New("initial balance cannot be negative")
	}

	walletID := req.ID
	if walletID == "" {
		walletID = uuid.NewString()
	}

	currency := req.Currency
	if currency == "" {
		currency = "USD"
	}

	now := time.Now().UTC()
	wallet := &domain.Wallet{
		ID:        walletID,
		Name:      req.Name,
		Balance:   req.InitialBalance,
		Currency:  currency,
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.repos.Wallets.CreateWallet(ctx, wallet); err != nil {
		return nil, fmt.Errorf("failed to create wallet: %w", err)
	}

	// If seeded with an initial balance, record an initial funding transfer and double-entry ledger pair
	if req.InitialBalance > 0 {
		transferID := uuid.NewString()
		depositTransfer := &domain.Transfer{
			ID:             transferID,
			IdempotencyKey: fmt.Sprintf("deposit-%s", wallet.ID),
			FromWalletID:   "system_treasury",
			ToWalletID:     wallet.ID,
			Amount:         req.InitialBalance,
			Status:         domain.TransferStatusProcessed,
			CreatedAt:      now,
			UpdatedAt:      now,
		}
		if err := s.repos.Transfers.CreateTransfer(ctx, depositTransfer); err != nil {
			return nil, fmt.Errorf("failed to create deposit transfer: %w", err)
		}

		debitTreasury, creditWallet, _ := domain.NewDoubleEntryPair(transferID, "system_treasury", wallet.ID, req.InitialBalance)
		if err := s.repos.Ledger.CreateLedgerEntries(ctx, debitTreasury, creditWallet); err != nil {
			return nil, fmt.Errorf("failed to record initial deposit ledger entries: %w", err)
		}
	}

	return wallet, nil
}

// GetWallet returns a wallet by its ID.
func (s *WalletService) GetWallet(ctx context.Context, id string) (*domain.Wallet, error) {
	return s.repos.Wallets.GetWalletByID(ctx, id)
}

// GetWalletLedger retrieves all ledger entries for a wallet.
func (s *WalletService) GetWalletLedger(ctx context.Context, walletID string) ([]domain.LedgerEntry, error) {
	// Ensure wallet exists
	if _, err := s.repos.Wallets.GetWalletByID(ctx, walletID); err != nil {
		return nil, err
	}
	return s.repos.Ledger.GetLedgerByWalletID(ctx, walletID)
}

// ReconcileBalance audits stored balance against the sum of historical ledger entries.
func (s *WalletService) ReconcileBalance(ctx context.Context, walletID string) (storedBalance int64, ledgerBalance int64, isBalanced bool, err error) {
	wallet, err := s.repos.Wallets.GetWalletByID(ctx, walletID)
	if err != nil {
		return 0, 0, false, err
	}

	ledgerBal, err := s.repos.Ledger.CalculateLedgerBalance(ctx, walletID)
	if err != nil {
		return 0, 0, false, err
	}

	isBalanced = wallet.Balance == ledgerBal
	return wallet.Balance, ledgerBal, isBalanced, nil
}
