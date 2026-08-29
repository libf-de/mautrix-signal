package store_test

import (
	"context"
	"database/sql"
	"io"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/require"
	"go.mau.fi/util/dbutil"

	"go.mau.fi/mautrix-signal/pkg/signalmeow/store"
)

// TestUpgradesApply checks that the schema (including the story distribution list table) is
// created from scratch without errors.
func TestUpgradesApply(t *testing.T) {
	ctx := context.Background()
	rawDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "test.db")+"?_foreign_keys=on")
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	db, err := dbutil.NewWithDB(rawDB, "sqlite3")
	require.NoError(t, err)

	container := store.NewStore(db, dbutil.ZeroLogger(zerologNop()))
	require.NoError(t, container.Upgrade(ctx))

	var name string
	err = rawDB.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type='table' AND name='signalmeow_story_distribution_list'",
	).Scan(&name)
	require.NoError(t, err, "story distribution list table should exist")
	require.Equal(t, "signalmeow_story_distribution_list", name)

	// Sanity check that the my-story UUID is the all-zero one.
	require.Equal(t, uuid.UUID{}, store.MyStoryID)
}

// TestStoryDistributionListRoundTrip covers the custom Scan/Value handling for the UUID primary
// key and the JSON recipient list.
func TestStoryDistributionListRoundTrip(t *testing.T) {
	ctx := context.Background()
	rawDB, err := sql.Open("sqlite3", filepath.Join(t.TempDir(), "test.db")+"?_foreign_keys=on")
	require.NoError(t, err)
	t.Cleanup(func() { _ = rawDB.Close() })
	db, err := dbutil.NewWithDB(rawDB, "sqlite3")
	require.NoError(t, err)
	container := store.NewStore(db, dbutil.ZeroLogger(zerologNop()))
	require.NoError(t, container.Upgrade(ctx))

	aci := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	_, err = rawDB.ExecContext(ctx, `
		INSERT INTO signalmeow_device (
			aci_uuid, aci_identity_key_pair, registration_id,
			pni_uuid, pni_identity_key_pair, pni_registration_id, device_id
		) VALUES ($1, $2, 1, $3, $4, 1, 1)
	`, aci.String(), []byte{1}, uuid.MustParse("22222222-2222-2222-2222-222222222222").String(), []byte{2})
	require.NoError(t, err)

	st := store.NewSQLStoreForTest(container, aci)

	got, err := st.GetStoryDistributionList(ctx, store.MyStoryID)
	require.NoError(t, err)
	require.Nil(t, got, "unknown list should return nil, not an error")

	list := &store.StoryDistributionList{
		ID:            store.MyStoryID,
		Name:          "My Story",
		AllowsReplies: true,
		IsBlockList:   true,
		Recipients:    []string{"33333333-3333-3333-3333-333333333333"},
	}
	require.NoError(t, st.PutStoryDistributionList(ctx, list))

	got, err = st.GetStoryDistributionList(ctx, store.MyStoryID)
	require.NoError(t, err)
	require.NotNil(t, got)
	require.Equal(t, list.ID, got.ID)
	require.Equal(t, "My Story", got.Name)
	require.True(t, got.AllowsReplies)
	require.True(t, got.IsBlockList)
	require.Equal(t, []string{"33333333-3333-3333-3333-333333333333"}, got.Recipients)
	require.False(t, got.IsDeleted())

	// Upsert should replace the recipient list rather than duplicating the row.
	list.Recipients = nil
	list.DeletedAtTS = 1234
	require.NoError(t, st.PutStoryDistributionList(ctx, list))
	all, err := st.GetAllStoryDistributionLists(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	require.Empty(t, all[0].Recipients)
	require.True(t, all[0].IsDeleted())
}

func zerologNop() zerolog.Logger {
	return zerolog.New(io.Discard)
}
