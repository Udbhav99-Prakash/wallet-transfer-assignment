package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"wallet-transfer-assignment/internal/domain"
	"wallet-transfer-assignment/internal/repository"
)

type idempotencyRepository struct {
	db DBTX
}

// NewIdempotencyRepository returns a repository.IdempotencyRepository backed by PostgreSQL.
func NewIdempotencyRepository(db DBTX) repository.IdempotencyRepository {
	return &idempotencyRepository{db: db}
}

func (r *idempotencyRepository) ReserveIdempotency(ctx context.Context, record *domain.IdempotencyRecord) (*domain.IdempotencyRecord, bool, error) {
	now := time.Now().UTC()
	record.CreatedAt = now
	record.UpdatedAt = now

	insertQuery := `
		INSERT INTO idempotency_records (idempotency_key, request_hash, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING idempotency_key
	`
	var insertedKey string
	err := r.db.QueryRow(ctx, insertQuery,
		record.IdempotencyKey,
		record.RequestHash,
		string(record.Status),
		record.CreatedAt,
		record.UpdatedAt,
	).Scan(&insertedKey)

	if err == nil {
		// Successfully inserted new record
		return record, true, nil
	}

	if errors.Is(err, pgx.ErrNoRows) {
		// Conflict: key already exists. Fetch the existing record.
		existing, getErr := r.GetIdempotency(ctx, record.IdempotencyKey)
		if getErr != nil {
			return nil, false, fmt.Errorf("failed to fetch conflicting idempotency record: %w", getErr)
		}
		return existing, false, nil
	}

	return nil, false, fmt.Errorf("failed to reserve idempotency key: %w", err)
}

func (r *idempotencyRepository) GetIdempotency(ctx context.Context, key string) (*domain.IdempotencyRecord, error) {
	query := `
		SELECT idempotency_key, request_hash, transfer_id, status, response_code, response_body, created_at, updated_at
		FROM idempotency_records
		WHERE idempotency_key = $1
	`
	var rec domain.IdempotencyRecord
	var statusStr string
	var transferID *string
	var respCode *int
	var respBody *string

	err := r.db.QueryRow(ctx, query, key).Scan(
		&rec.IdempotencyKey,
		&rec.RequestHash,
		&transferID,
		&statusStr,
		&respCode,
		&respBody,
		&rec.CreatedAt,
		&rec.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get idempotency record: %w", err)
	}

	rec.Status = domain.IdempotencyStatus(statusStr)
	rec.TransferID = transferID
	if respCode != nil {
		rec.ResponseCode = *respCode
	}
	if respBody != nil {
		rec.ResponseBody = *respBody
	}
	return &rec, nil
}

func (r *idempotencyRepository) UpdateIdempotency(ctx context.Context, record *domain.IdempotencyRecord) error {
	query := `
		UPDATE idempotency_records
		SET transfer_id = $1, status = $2, response_code = $3, response_body = $4, updated_at = $5
		WHERE idempotency_key = $6
	`
	_, err := r.db.Exec(ctx, query,
		record.TransferID,
		string(record.Status),
		record.ResponseCode,
		record.ResponseBody,
		time.Now().UTC(),
		record.IdempotencyKey,
	)
	if err != nil {
		return fmt.Errorf("failed to update idempotency record: %w", err)
	}
	return nil
}
