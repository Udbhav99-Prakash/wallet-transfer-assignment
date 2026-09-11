package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

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

func (r *idempotencyRepository) ReserveIdempotency(ctx context.Context, record *domain.IdempotencyRecord, staleTimeout time.Duration) (*domain.IdempotencyRecord, bool, error) {
	now := time.Now().UTC()
	record.CreatedAt = now
	record.UpdatedAt = now

	insertQuery := `
		INSERT INTO idempotency_records (idempotency_key, request_hash, owner_token, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (idempotency_key) DO NOTHING
		RETURNING idempotency_key
	`
	var insertedKey string
	err := r.db.QueryRow(ctx, insertQuery,
		record.IdempotencyKey,
		record.RequestHash,
		record.OwnerToken,
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
		if existing == nil {
			return nil, false, fmt.Errorf("idempotency record disappeared during reservation")
		}

		// Explicit stale-owner recovery with owner token fencing:
		// If existing record is IN_PROGRESS and hasn't been updated for longer than staleTimeout,
		// the previous owner likely crashed or timed out. Attempt to reclaim it atomically.
		if staleTimeout > 0 && existing.Status == domain.IdempotencyStatusInProgress && now.Sub(existing.UpdatedAt) > staleTimeout {
			reclaimQuery := `
				UPDATE idempotency_records
				SET request_hash = $1, owner_token = $2, status = $3, updated_at = $4
				WHERE idempotency_key = $5 AND status = 'IN_PROGRESS' AND updated_at <= $6
				RETURNING idempotency_key
			`
			var reclaimedKey string
			reclaimErr := r.db.QueryRow(ctx, reclaimQuery,
				record.RequestHash,
				record.OwnerToken,
				string(domain.IdempotencyStatusInProgress),
				now,
				record.IdempotencyKey,
				existing.UpdatedAt,
			).Scan(&reclaimedKey)

			if reclaimErr == nil {
				// Successfully reclaimed stale reservation
				record.CreatedAt = existing.CreatedAt
				record.UpdatedAt = now
				return record, true, nil
			}

			if errors.Is(reclaimErr, pgx.ErrNoRows) {
				// Another worker reclaimed or completed it concurrently; fetch latest state
				refetched, refetchErr := r.GetIdempotency(ctx, record.IdempotencyKey)
				if refetchErr != nil {
					return nil, false, fmt.Errorf("failed to refetch idempotency record after reclaim conflict: %w", refetchErr)
				}
				if refetched == nil {
					return nil, false, fmt.Errorf("idempotency record disappeared after reclaim conflict")
				}
				return refetched, false, nil
			}

			return nil, false, fmt.Errorf("failed to reclaim stale idempotency reservation: %w", reclaimErr)
		}

		return existing, false, nil
	}

	return nil, false, fmt.Errorf("failed to reserve idempotency key: %w", err)
}

func (r *idempotencyRepository) GetIdempotency(ctx context.Context, key string) (*domain.IdempotencyRecord, error) {
	query := `
		SELECT idempotency_key, request_hash, owner_token, transfer_id, status, response_code, response_body, created_at, updated_at
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
		&rec.OwnerToken,
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
	var query string
	var cmdTag pgconn.CommandTag
	var err error

	if record.OwnerToken != "" {
		query = `
			UPDATE idempotency_records
			SET transfer_id = $1, status = $2, response_code = $3, response_body = $4, updated_at = $5
			WHERE idempotency_key = $6 AND owner_token = $7
		`
		cmdTag, err = r.db.Exec(ctx, query,
			record.TransferID,
			string(record.Status),
			record.ResponseCode,
			record.ResponseBody,
			time.Now().UTC(),
			record.IdempotencyKey,
			record.OwnerToken,
		)
	} else {
		query = `
			UPDATE idempotency_records
			SET transfer_id = $1, status = $2, response_code = $3, response_body = $4, updated_at = $5
			WHERE idempotency_key = $6
		`
		cmdTag, err = r.db.Exec(ctx, query,
			record.TransferID,
			string(record.Status),
			record.ResponseCode,
			record.ResponseBody,
			time.Now().UTC(),
			record.IdempotencyKey,
		)
	}

	if err != nil {
		return fmt.Errorf("failed to update idempotency record: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return domain.ErrIdempotencyLeaseLost
	}
	return nil
}

func (r *idempotencyRepository) DeleteIdempotency(ctx context.Context, key string) error {
	query := `
		DELETE FROM idempotency_records
		WHERE idempotency_key = $1 AND status = 'IN_PROGRESS'
	`
	_, err := r.db.Exec(ctx, query, key)
	if err != nil {
		return fmt.Errorf("failed to delete in-progress idempotency reservation: %w", err)
	}
	return nil
}

func (r *idempotencyRepository) DeleteInProgress(ctx context.Context, key string, ownerToken string) error {
	query := `
		DELETE FROM idempotency_records
		WHERE idempotency_key = $1 AND status = 'IN_PROGRESS' AND owner_token = $2
	`
	_, err := r.db.Exec(ctx, query, key, ownerToken)
	if err != nil {
		return fmt.Errorf("failed to delete in-progress idempotency reservation: %w", err)
	}
	return nil
}

