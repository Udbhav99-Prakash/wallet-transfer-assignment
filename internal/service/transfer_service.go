package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/repository"
)

// TransferService provides transactional wallet transfer operations.
type TransferService struct {
	txManager repository.TxManager
	repos     repository.Repositories
}

// NewTransferService constructs a TransferService.
func NewTransferService(txManager repository.TxManager, repos repository.Repositories) *TransferService {
	return &TransferService{
		txManager: txManager,
		repos:     repos,
	}
}

// ExecuteTransfer orchestrates a safe, idempotent, double-entry wallet transfer.
func (s *TransferService) ExecuteTransfer(ctx context.Context, req CreateTransferRequest) (*TransferResponse, error) {
	// 1. Basic validation
	if req.IdempotencyKey == "" {
		return nil, errors.New("idempotencyKey is required")
	}
	if req.FromWalletID == "" || req.ToWalletID == "" {
		return nil, domain.ErrWalletNotFound
	}
	if req.FromWalletID == req.ToWalletID {
		return nil, domain.ErrSameWalletTransfer
	}
	if req.Amount <= 0 {
		return nil, domain.ErrInvalidAmount
	}

	requestHash := domain.ComputeRequestHash(req.FromWalletID, req.ToWalletID, req.Amount)

	// 2. Pre-check existing idempotency record outside transaction for fast replay
	existing, err := s.repos.Idempotency.GetIdempotency(ctx, req.IdempotencyKey)
	if err != nil {
		return nil, fmt.Errorf("error checking idempotency: %w", err)
	}
	if existing != nil {
		if existing.RequestHash != requestHash {
			return nil, domain.ErrIdempotencyConflict
		}
		if existing.Status == domain.IdempotencyStatusCompleted || existing.Status == domain.IdempotencyStatusFailed {
			var cachedResp TransferResponse
			if jsonErr := json.Unmarshal([]byte(existing.ResponseBody), &cachedResp); jsonErr == nil {
				cachedResp.IsReplay = true
				if existing.Status == domain.IdempotencyStatusFailed {
					return &cachedResp, domain.ErrInsufficientFunds
				}
				return &cachedResp, nil
			}
		}
		if existing.Status == domain.IdempotencyStatusInProgress {
			return nil, domain.ErrIdempotencyInProgress
		}
	}

	// 3. Execute transfer inside transaction
	var finalResponse *TransferResponse
	var isInsufficientFunds bool

	txErr := s.txManager.ExecuteInTx(ctx, func(txRepos repository.Repositories) error {
		// Reserve idempotency key inside transaction
		record := &domain.IdempotencyRecord{
			IdempotencyKey: req.IdempotencyKey,
			RequestHash:    requestHash,
			Status:         domain.IdempotencyStatusInProgress,
		}
		reservedRecord, isNew, reserveErr := txRepos.Idempotency.ReserveIdempotency(ctx, record)
		if reserveErr != nil {
			return reserveErr
		}
		if !isNew {
			if reservedRecord.RequestHash != requestHash {
				return domain.ErrIdempotencyConflict
			}
			if reservedRecord.Status == domain.IdempotencyStatusCompleted || reservedRecord.Status == domain.IdempotencyStatusFailed {
				var cachedResp TransferResponse
				if jsonErr := json.Unmarshal([]byte(reservedRecord.ResponseBody), &cachedResp); jsonErr == nil {
					cachedResp.IsReplay = true
					finalResponse = &cachedResp
					if reservedRecord.Status == domain.IdempotencyStatusFailed {
						isInsufficientFunds = true
					}
					return nil
				}
			}
			return domain.ErrIdempotencyInProgress
		}

		// Deterministic lock ordering to prevent deadlocks:
		// Always acquire locks in alphabetical order: min(fromID, toID) followed by max(fromID, toID)
		firstID, secondID := req.FromWalletID, req.ToWalletID
		if firstID > secondID {
			firstID, secondID = secondID, firstID
		}

		wallet1, lockErr1 := txRepos.Wallets.GetWalletByIDForUpdate(ctx, firstID)
		if lockErr1 != nil {
			return lockErr1
		}
		wallet2, lockErr2 := txRepos.Wallets.GetWalletByIDForUpdate(ctx, secondID)
		if lockErr2 != nil {
			return lockErr2
		}

		var fromWallet, toWallet *domain.Wallet
		if wallet1.ID == req.FromWalletID {
			fromWallet, toWallet = wallet1, wallet2
		} else {
			fromWallet, toWallet = wallet2, wallet1
		}

		// Initialize transfer record with PENDING state
		transferID := uuid.NewString()
		transfer := &domain.Transfer{
			ID:             transferID,
			IdempotencyKey: req.IdempotencyKey,
			FromWalletID:   fromWallet.ID,
			ToWalletID:     toWallet.ID,
			Amount:         req.Amount,
			Status:         domain.TransferStatusPending,
		}
		if createErr := txRepos.Transfers.CreateTransfer(ctx, transfer); createErr != nil {
			return createErr
		}

		// Check balance for debit
		if !fromWallet.CanDebit(req.Amount) {
			reason := "insufficient funds in source wallet"
			_ = transfer.MarkFailed(reason)
			_ = txRepos.Transfers.UpdateTransferStatus(ctx, transfer.ID, domain.TransferStatusFailed, &reason)

			resp := &TransferResponse{
				TransferID:     transfer.ID,
				IdempotencyKey: transfer.IdempotencyKey,
				FromWalletID:   transfer.FromWalletID,
				ToWalletID:     transfer.ToWalletID,
				Amount:         transfer.Amount,
				Status:         domain.TransferStatusFailed,
				FailureReason:  &reason,
				CreatedAt:      transfer.CreatedAt,
			}
			respBytes, _ := json.Marshal(resp)

			record.Status = domain.IdempotencyStatusFailed
			record.TransferID = &transfer.ID
			record.ResponseCode = 422
			record.ResponseBody = string(respBytes)
			_ = txRepos.Idempotency.UpdateIdempotency(ctx, record)

			finalResponse = resp
			isInsufficientFunds = true
			// Return nil so the transaction commits the FAILED transfer record and idempotency record
			return nil
		}

		// Sufficient balance: update balances
		if debitErr := fromWallet.Debit(req.Amount); debitErr != nil {
			return debitErr
		}
		if creditErr := toWallet.Credit(req.Amount); creditErr != nil {
			return creditErr
		}

		if updateErr := txRepos.Wallets.UpdateWalletBalance(ctx, fromWallet.ID, fromWallet.Balance); updateErr != nil {
			return updateErr
		}
		if updateErr := txRepos.Wallets.UpdateWalletBalance(ctx, toWallet.ID, toWallet.Balance); updateErr != nil {
			return updateErr
		}

		// Create balanced double-entry ledger records
		debitEntry, creditEntry, ledgerErr := domain.NewDoubleEntryPair(transfer.ID, fromWallet.ID, toWallet.ID, req.Amount)
		if ledgerErr != nil {
			return ledgerErr
		}
		if err := txRepos.Ledger.CreateLedgerEntries(ctx, debitEntry, creditEntry); err != nil {
			return err
		}

		// Transition transfer to PROCESSED
		if markErr := transfer.MarkProcessed(); markErr != nil {
			return markErr
		}
		if err := txRepos.Transfers.UpdateTransferStatus(ctx, transfer.ID, domain.TransferStatusProcessed, nil); err != nil {
			return err
		}

		// Update idempotency record to COMPLETED with cached response body
		resp := &TransferResponse{
			TransferID:     transfer.ID,
			IdempotencyKey: transfer.IdempotencyKey,
			FromWalletID:   transfer.FromWalletID,
			ToWalletID:     transfer.ToWalletID,
			Amount:         transfer.Amount,
			Status:         domain.TransferStatusProcessed,
			CreatedAt:      transfer.CreatedAt,
		}
		respBytes, _ := json.Marshal(resp)

		record.Status = domain.IdempotencyStatusCompleted
		record.TransferID = &transfer.ID
		record.ResponseCode = 200
		record.ResponseBody = string(respBytes)
		if updateErr := txRepos.Idempotency.UpdateIdempotency(ctx, record); updateErr != nil {
			return updateErr
		}

		finalResponse = resp
		return nil
	})

	if txErr != nil {
		return nil, txErr
	}

	if isInsufficientFunds {
		return finalResponse, domain.ErrInsufficientFunds
	}

	return finalResponse, nil
}

// GetTransfer retrieves transfer details by ID.
func (s *TransferService) GetTransfer(ctx context.Context, id string) (*domain.Transfer, error) {
	return s.repos.Transfers.GetTransferByID(ctx, id)
}
