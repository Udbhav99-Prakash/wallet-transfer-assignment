package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/repository"
)

type ledgerRepository struct {
	db DBTX
}

// NewLedgerRepository returns a repository.LedgerRepository backed by PostgreSQL.
func NewLedgerRepository(db DBTX) repository.LedgerRepository {
	return &ledgerRepository{db: db}
}

func (r *ledgerRepository) CreateLedgerEntries(ctx context.Context, entries ...domain.LedgerEntry) error {
	if len(entries) == 0 {
		return nil
	}

	query := `
		INSERT INTO ledger_entries (id, transfer_id, wallet_id, type, amount, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	for _, entry := range entries {
		id := entry.ID
		if id == "" {
			id = uuid.NewString()
		}
		now := entry.CreatedAt
		if now.IsZero() {
			now = time.Now().UTC()
		}
		var transferID *string
		if entry.TransferID != "" {
			transferID = &entry.TransferID
		}
		_, err := r.db.Exec(ctx, query,
			id,
			transferID,
			entry.WalletID,
			string(entry.Type),
			entry.Amount,
			now,
		)
		if err != nil {
			return fmt.Errorf("failed to create ledger entry: %w", err)
		}
	}
	return nil
}

func (r *ledgerRepository) GetLedgerByWalletID(ctx context.Context, walletID string) ([]domain.LedgerEntry, error) {
	query := `
		SELECT id, COALESCE(transfer_id, ''), wallet_id, type, amount, created_at
		FROM ledger_entries
		WHERE wallet_id = $1
		ORDER BY created_at ASC
	`
	rows, err := r.db.Query(ctx, query, walletID)
	if err != nil {
		return nil, fmt.Errorf("failed to query ledger entries by wallet: %w", err)
	}
	defer rows.Close()

	var entries []domain.LedgerEntry
	for rows.Next() {
		var e domain.LedgerEntry
		var typeStr string
		if err := rows.Scan(&e.ID, &e.TransferID, &e.WalletID, &typeStr, &e.Amount, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan ledger entry: %w", err)
		}
		e.Type = domain.LedgerEntryType(typeStr)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row error reading ledger entries: %w", err)
	}
	return entries, nil
}

func (r *ledgerRepository) GetLedgerByTransferID(ctx context.Context, transferID string) ([]domain.LedgerEntry, error) {
	query := `
		SELECT id, transfer_id, wallet_id, type, amount, created_at
		FROM ledger_entries
		WHERE transfer_id = $1
		ORDER BY type ASC
	`
	rows, err := r.db.Query(ctx, query, transferID)
	if err != nil {
		return nil, fmt.Errorf("failed to query ledger entries by transfer: %w", err)
	}
	defer rows.Close()

	var entries []domain.LedgerEntry
	for rows.Next() {
		var e domain.LedgerEntry
		var typeStr string
		if err := rows.Scan(&e.ID, &e.TransferID, &e.WalletID, &typeStr, &e.Amount, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan ledger entry: %w", err)
		}
		e.Type = domain.LedgerEntryType(typeStr)
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row error reading ledger entries: %w", err)
	}
	return entries, nil
}

// CalculateBalance computes the derived balance from all historical ledger entries.
func (r *ledgerRepository) CalculateLedgerBalance(ctx context.Context, walletID string) (int64, error) {
	query := `
		SELECT COALESCE(SUM(CASE WHEN type = 'CREDIT' THEN amount ELSE -amount END), 0)
		FROM ledger_entries
		WHERE wallet_id = $1
	`
	var balance int64
	if err := r.db.QueryRow(ctx, query, walletID).Scan(&balance); err != nil {
		return 0, fmt.Errorf("failed to calculate balance from ledger: %w", err)
	}
	return balance, nil
}
