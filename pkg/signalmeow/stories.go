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

package signalmeow

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"go.mau.fi/util/ptr"

	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	signalpb "go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/store"
)

// MyStoryID is the fixed UUID of the built-in "My Story" distribution list.
var MyStoryID = store.MyStoryID

func storyDistributionListFromRecord(record *signalpb.StoryDistributionListRecord) (*store.StoryDistributionList, error) {
	if len(record.GetIdentifier()) != 16 {
		return nil, fmt.Errorf("invalid distribution list identifier length %d", len(record.GetIdentifier()))
	}
	list := &store.StoryDistributionList{
		ID:            uuid.UUID(record.GetIdentifier()),
		Name:          record.GetName(),
		AllowsReplies: record.GetAllowsReplies(),
		IsBlockList:   record.GetIsBlockList(),
		DeletedAtTS:   record.GetDeletedAtTimestamp(),
		Recipients:    make([]string, 0, len(record.GetRecipientServiceIds())),
	}
	for _, rawServiceID := range record.GetRecipientServiceIds() {
		serviceID, err := libsignalgo.ServiceIDFromString(rawServiceID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse recipient service ID: %w", err)
		}
		list.Recipients = append(list.Recipients, serviceID.String())
	}
	for _, rawServiceID := range record.GetRecipientServiceIdsBinary() {
		serviceID, err := libsignalgo.ServiceIDFromBytes(rawServiceID)
		if err != nil {
			return nil, fmt.Errorf("failed to parse binary recipient service ID: %w", err)
		}
		list.Recipients = append(list.Recipients, serviceID.String())
	}
	return list, nil
}

// ResolveStoryRecipients returns the ACIs a story sent to the given distribution list should go to.
//
// "My Story" is normally stored as a block list, in which case the recipients are the user's Signal
// Connections (whitelisted recipients) minus the blocked ones. This mirrors
// sendStoryMessage in Signal Desktop.
func (cli *Client) ResolveStoryRecipients(ctx context.Context, listID uuid.UUID) ([]libsignalgo.ServiceID, error) {
	list, err := cli.Store.StoryStore.GetStoryDistributionList(ctx, listID)
	if err != nil {
		return nil, fmt.Errorf("failed to get story distribution list: %w", err)
	} else if list == nil {
		return nil, fmt.Errorf("story distribution list %s not found (has storage sync run?)", listID)
	} else if list.IsDeleted() {
		return nil, fmt.Errorf("story distribution list %s has been deleted", listID)
	}

	explicit := make(map[libsignalgo.ServiceID]struct{}, len(list.Recipients))
	for _, raw := range list.Recipients {
		serviceID, err := libsignalgo.ServiceIDFromString(raw)
		if err != nil {
			zerolog.Ctx(ctx).Warn().Err(err).Str("raw_service_id", raw).
				Msg("Failed to parse stored story recipient")
			continue
		}
		explicit[serviceID] = struct{}{}
	}

	if !list.IsBlockList {
		recipients := make([]libsignalgo.ServiceID, 0, len(explicit))
		for serviceID := range explicit {
			recipients = append(recipients, serviceID)
		}
		return recipients, nil
	}

	// Block list mode: everyone we've shared our profile with, except the blocked ones.
	allRecipients, err := cli.Store.RecipientStore.LoadAllContacts(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load contacts for story recipients: %w", err)
	}
	recipients := make([]libsignalgo.ServiceID, 0, len(allRecipients))
	for _, recipient := range allRecipients {
		if recipient.ACI == uuid.Nil || recipient.ACI == cli.Store.ACI {
			continue
		} else if recipient.Whitelisted == nil || !*recipient.Whitelisted {
			continue
		} else if recipient.Blocked {
			continue
		}
		serviceID := libsignalgo.NewACIServiceID(recipient.ACI)
		if _, isBlocked := explicit[serviceID]; isBlocked {
			continue
		}
		recipients = append(recipients, serviceID)
	}
	return recipients, nil
}

// SendStory sends a story to the given recipients, scoping the sender key to the distribution list.
func (cli *Client) SendStory(
	ctx context.Context,
	story *signalpb.StoryMessage,
	listID uuid.UUID,
	recipients []libsignalgo.ServiceID,
	messageTimestamp uint64,
) (*GroupMessageSendResult, error) {
	if len(recipients) == 0 {
		return nil, fmt.Errorf("no recipients for story")
	}
	log := zerolog.Ctx(ctx).With().
		Str("action", "send story").
		Stringer("distribution_id", listID).
		Int("recipient_count", len(recipients)).
		Logger()
	ctx = log.WithContext(ctx)

	content := &signalpb.Content{
		Content: &signalpb.Content_StoryMessage{StoryMessage: story},
	}
	result, err := cli.sendToGroupWithSenderKey(
		ctx, storySenderKeyTarget(listID), recipients, SendEndorsementCache{},
		content, messageTimestamp, 0,
	)
	if err != nil {
		return nil, err
	}
	cli.sendStorySyncCopy(ctx, story, listID, messageTimestamp, result)
	return result, nil
}

// sendStorySyncCopy tells the user's other devices about a story we just posted. Unlike normal
// messages, the sync copy carries the per-recipient distribution list mapping.
func (cli *Client) sendStorySyncCopy(
	ctx context.Context,
	story *signalpb.StoryMessage,
	listID uuid.UUID,
	messageTimestamp uint64,
	result *GroupMessageSendResult,
) {
	listIDBytes := listID
	storyRecipients := make([]*signalpb.SyncMessage_Sent_StoryMessageRecipient, 0, len(result.SuccessfullySentTo))
	for _, sent := range result.SuccessfullySentTo {
		storyRecipients = append(storyRecipients, &signalpb.SyncMessage_Sent_StoryMessageRecipient{
			DestinationServiceIdBinary: sent.Recipient.Bytes(),
			DistributionListIds:        []string{listIDBytes.String()},
			IsAllowedToReply:           ptr.Ptr(story.GetAllowsReplies()),
		})
	}
	syncContent := syncSentMessage(&signalpb.SyncMessage_Sent{
		Timestamp:              ptr.Ptr(messageTimestamp),
		StoryMessage:           story,
		StoryMessageRecipients: storyRecipients,
	})
	_, err := cli.sendContentToSelf(ctx, messageTimestamp, syncContent, 0, true, nil, nil)
	if err != nil {
		zerolog.Ctx(ctx).Err(err).Msg("Failed to send story sync message to myself")
	}
}
