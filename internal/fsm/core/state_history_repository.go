package fsm

import (
	"context"

	"gorm.io/gorm"
)

// StateHistoryRepository defines operations for persisting and querying state history.
type StateHistoryRepository interface {
	// Create inserts a new state history record within the provided transaction.
	Create(ctx context.Context, tx *gorm.DB, history *StateHistory) error

	// CreateBatch inserts multiple state history records in a single INSERT within the
	// provided transaction. Used by the FSM Manager to efficiently record skipped states.
	// No-op if the slice is empty.
	CreateBatch(ctx context.Context, tx *gorm.DB, histories []StateHistory) error

	// GetHistoryByResourceID retrieves all state transitions for a specific resource,
	// ordered by creation time ASC (oldest first).
	GetHistoryByResourceID(ctx context.Context, db *gorm.DB, resourceID string, resourceType string) ([]StateHistory, error)

	// GetHistoryByResourceIDs retrieves state transitions for multiple resources in a single query,
	// ordered by resource_id, created_at ASC. Used for bulk timeline construction.
	GetHistoryByResourceIDs(ctx context.Context, db *gorm.DB, resourceIDs []string, resourceType string) ([]StateHistory, error)
}

// stateHistoryRepository implements StateHistoryRepository
type stateHistoryRepository struct {
	db *gorm.DB
}

// NewStateHistoryRepository creates a new StateHistoryRepository instance.
func NewStateHistoryRepository(db *gorm.DB) StateHistoryRepository {
	return &stateHistoryRepository{db: db}
}

// Create implements StateHistoryRepository
func (r *stateHistoryRepository) Create(ctx context.Context, tx *gorm.DB, history *StateHistory) error {
	if tx == nil {
		tx = r.db
	}
	return tx.WithContext(ctx).Create(history).Error
}

// CreateBatch implements StateHistoryRepository
func (r *stateHistoryRepository) CreateBatch(ctx context.Context, tx *gorm.DB, histories []StateHistory) error {
	if len(histories) == 0 {
		return nil
	}
	if tx == nil {
		tx = r.db
	}
	return tx.WithContext(ctx).Create(&histories).Error
}

// GetHistoryByResourceID implements StateHistoryRepository
func (r *stateHistoryRepository) GetHistoryByResourceID(ctx context.Context, db *gorm.DB, resourceID string, resourceType string) ([]StateHistory, error) {
	if db == nil {
		db = r.db
	}

	var history []StateHistory
	err := db.WithContext(ctx).
		Where("resource_id = ? AND resource_type = ?", resourceID, resourceType).
		Order("created_at ASC").
		Find(&history).Error

	return history, err
}

// GetHistoryByResourceIDs implements StateHistoryRepository
func (r *stateHistoryRepository) GetHistoryByResourceIDs(ctx context.Context, db *gorm.DB, resourceIDs []string, resourceType string) ([]StateHistory, error) {
	if db == nil {
		db = r.db
	}
	if len(resourceIDs) == 0 {
		return nil, nil
	}

	var history []StateHistory
	err := db.WithContext(ctx).
		Where("resource_id IN ? AND resource_type = ?", resourceIDs, resourceType).
		Order("resource_id, created_at ASC").
		Find(&history).Error

	return history, err
}
