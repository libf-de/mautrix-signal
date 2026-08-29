package store

import (
	"github.com/google/uuid"
)

// NewSQLStoreForTest exposes the internal per-account store to tests in the store_test package.
func NewSQLStoreForTest(c *Container, accountID uuid.UUID) StoryDistributionListStore {
	return &sqlStore{Container: c, AccountID: accountID, blockCache: make(map[uuid.UUID]bool)}
}
