// mautrix-signal - A Matrix-Signal puppeting bridge.
// Copyright (C) 2026 Tulir Asokan
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU Affero General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU Affero General Public License for more details.
//
// You should have received a copy of the GNU Affero General Public License
// along with this program.  If not, see <https://www.gnu.org/licenses/>.

package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"
	"go.mau.fi/util/dbutil"
)

var _ StoryDistributionListStore = (*sqlStore)(nil)

// MyStoryID is the fixed identifier of the built-in "My Story" distribution list.
var MyStoryID = uuid.UUID{}

// StoryDistributionList is a story distribution list from the storage service.
type StoryDistributionList struct {
	ID   uuid.UUID
	Name string
	// AllowsReplies is whether recipients may reply to stories sent to this list.
	AllowsReplies bool
	// IsBlockList inverts the meaning of Recipients: they're excluded rather than included.
	// Only meaningful for MyStoryID.
	IsBlockList bool
	// Recipients holds ACIs, as service ID strings.
	Recipients []string
	// DeletedAtTS is non-zero if the list has been deleted.
	DeletedAtTS uint64
}

func (sdl *StoryDistributionList) IsDeleted() bool {
	return sdl.DeletedAtTS != 0
}

type StoryDistributionListStore interface {
	GetStoryDistributionList(ctx context.Context, id uuid.UUID) (*StoryDistributionList, error)
	GetAllStoryDistributionLists(ctx context.Context) ([]*StoryDistributionList, error)
	PutStoryDistributionList(ctx context.Context, list *StoryDistributionList) error
}

const (
	storyDistributionListColumns = `distribution_id, name, allows_replies, is_block_list, recipients, deleted_at_ts`

	getStoryDistributionListQuery = `
		SELECT ` + storyDistributionListColumns + `
		FROM signalmeow_story_distribution_list WHERE account_id=$1 AND distribution_id=$2
	`
	getAllStoryDistributionListsQuery = `
		SELECT ` + storyDistributionListColumns + `
		FROM signalmeow_story_distribution_list WHERE account_id=$1
	`
	upsertStoryDistributionListQuery = `
		INSERT INTO signalmeow_story_distribution_list (
			account_id, distribution_id, name, allows_replies, is_block_list, recipients, deleted_at_ts
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (account_id, distribution_id) DO UPDATE SET
			name = excluded.name,
			allows_replies = excluded.allows_replies,
			is_block_list = excluded.is_block_list,
			recipients = excluded.recipients,
			deleted_at_ts = excluded.deleted_at_ts
	`
)

func scanStoryDistributionList(row dbutil.Scannable) (*StoryDistributionList, error) {
	var list StoryDistributionList
	err := row.Scan(
		&list.ID, &list.Name, &list.AllowsReplies, &list.IsBlockList,
		dbutil.JSON{Data: &list.Recipients}, &list.DeletedAtTS,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	return &list, nil
}

func (s *sqlStore) GetStoryDistributionList(ctx context.Context, id uuid.UUID) (*StoryDistributionList, error) {
	return scanStoryDistributionList(s.db.QueryRow(ctx, getStoryDistributionListQuery, s.AccountID, id))
}

func (s *sqlStore) GetAllStoryDistributionLists(ctx context.Context) ([]*StoryDistributionList, error) {
	rows, err := s.db.Query(ctx, getAllStoryDistributionListsQuery, s.AccountID)
	if err != nil {
		return nil, err
	}
	return dbutil.NewRowIter(rows, scanStoryDistributionList).AsList()
}

func (s *sqlStore) PutStoryDistributionList(ctx context.Context, list *StoryDistributionList) error {
	recipients := list.Recipients
	if recipients == nil {
		recipients = []string{}
	}
	_, err := s.db.Exec(
		ctx, upsertStoryDistributionListQuery,
		s.AccountID, list.ID, list.Name, list.AllowsReplies, list.IsBlockList,
		dbutil.JSON{Data: &recipients}, list.DeletedAtTS,
	)
	return err
}
