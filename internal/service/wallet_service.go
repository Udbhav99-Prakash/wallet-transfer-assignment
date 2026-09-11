package service

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/repository"
)

// WalletService provides wallet inspection, creation, and ledger balance reconciliation.
type WalletService struct {
	txManager repository.TxManager
	repos     repository.Repositories
}

// NewWalletService constructs a WalletService.
func NewWalletService(txManager repository.TxManager, repos repository.Repositories) *WalletService {
	return &WalletService{txManager: txManager, repos: repos}
}

// CreateWallet creates a new wallet with an initial balance and initial credit ledger record if balance > 0.
func (s *WalletService) CreateWallet(ctx context.Context, req CreateWalletRequest) (*domain.Wallet, error) {
	if req.Name == "" {
		return nil, domain.ErrWalletNameRequired
	}
	if req.InitialBalance < 0 {
		return nil, domain.ErrNegativeBalance
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

	createOp := func(repos repository.Repositories) error {
		if err := repos.Wallets.CreateWallet(ctx, wallet); err != nil {
			return fmt.Errorf("failed to create wallet: %w", err)
		}

		// If seeded with an initial balance, record an initial funding transfer, double-entry ledger pair,
		// and debit system_treasury so both wallets remain fully reconciled with their ledger entries.
		if req.InitialBalance > 0 {
			treasury, err := repos.Wallets.GetWalletByIDForUpdate(ctx, "system_treasury")
			if err != nil {
				return fmt.Errorf("failed to fetch system treasury wallet: %w", err)
			}
			if treasury.Currency != wallet.Currency {
				return fmt.Errorf("initial funding from system treasury (%s) to wallet (%s) not supported: %w", treasury.Currency, wallet.Currency, domain.ErrCurrencyMismatch)
			}
			if err := treasury.Debit(req.InitialBalance); err != nil {
				return fmt.Errorf("failed to debit system treasury: %w", err)
			}
			if err := repos.Wallets.UpdateWalletBalance(ctx, treasury.ID, treasury.Balance); err != nil {
				return fmt.Errorf("failed to update system treasury balance: %w", err)
			}

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
			if err := repos.Transfers.CreateTransfer(ctx, depositTransfer); err != nil {
				return fmt.Errorf("failed to create deposit transfer: %w", err)
			}

			debitTreasury, creditWallet, _ := domain.NewDoubleEntryPair(transferID, "system_treasury", wallet.ID, req.InitialBalance)
			if err := repos.Ledger.CreateLedgerEntries(ctx, debitTreasury, creditWallet); err != nil {
				return fmt.Errorf("failed to record initial deposit ledger entries: %w", err)
			}
		}
		return nil
	}

	if s.txManager != nil {
		if err := s.txManager.ExecuteInTx(ctx, createOp); err != nil {
			return nil, err
		}
	} else {
		if err := createOp(s.repos); err != nil {
			return nil, err
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
// Acquires a row lock on the wallet within a transaction so concurrent transfers cannot commit
// between the stored balance read and the ledger calculation, preventing false reconciliation mismatches.
func (s *WalletService) ReconcileBalance(ctx context.Context, walletID string) (storedBalance int64, ledgerBalance int64, isBalanced bool, err error) {
	reconcileOp := func(repos repository.Repositories) error {
		var wallet *domain.Wallet
		var getErr error
		if s.txManager != nil {
			wallet, getErr = repos.Wallets.GetWalletByIDForUpdate(ctx, walletID)
		} else {
			wallet, getErr = repos.Wallets.GetWalletByID(ctx, walletID)
		}
		if getErr != nil {
			return getErr
		}

		ledgerBal, calcErr := repos.Ledger.CalculateLedgerBalance(ctx, walletID)
		if calcErr != nil {
			return calcErr
		}

		storedBalance = wallet.Balance
		ledgerBalance = ledgerBal
		isBalanced = storedBalance == ledgerBalance
		return nil
	}

	if s.txManager != nil {
		if err := s.txManager.ExecuteInTx(ctx, reconcileOp); err != nil {
			return 0, 0, false, err
		}
	} else {
		if err := reconcileOp(s.repos); err != nil {
			return 0, 0, false, err
		}
	}
	return storedBalance, ledgerBalance, isBalanced, nil
}
