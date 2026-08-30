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

package connector

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/bridgev2/database"
	"maunium.net/go/mautrix/bridgev2/networkid"

	"go.mau.fi/mautrix-signal/pkg/libsignalgo"
	"go.mau.fi/mautrix-signal/pkg/signalid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	signalpb "go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf"
	"go.mau.fi/mautrix-signal/pkg/signalmeow/types"
)

var (
	ErrStorySendDisabled = errors.New("sending stories is disabled in the bridge config")
	ErrNotAStory         = errors.New("target message is not a story")
	ErrStoryNoReplies    = errors.New("this story doesn't allow replies")
	ErrOwnStory          = errors.New("can't reply to or react to your own story")
)

// storyMetadata pulls the story fields off a bridged story message, erroring if the message isn't
// a story.
func storyMetadata(msg *database.Message) (*signalid.MessageMetadata, error) {
	if msg == nil {
		return nil, ErrNotAStory
	}
	meta, ok := msg.Metadata.(*signalid.MessageMetadata)
	if !ok || meta.StoryAuthor == "" {
		return nil, ErrNotAStory
	}
	return meta, nil
}

func storyContextFor(meta *signalid.MessageMetadata) (*signalpb.DataMessage_StoryContext, error) {
	authorACI, err := uuid.Parse(meta.StoryAuthor)
	if err != nil {
		return nil, fmt.Errorf("failed to parse story author: %w", err)
	}
	return &signalpb.DataMessage_StoryContext{
		AuthorAciBinary: authorACI[:],
		SentTimestamp:   proto.Uint64(meta.StorySentTimestamp),
	}, nil
}

// sendToStoryAuthor sends a story reply or reaction to wherever the story came from: the group for
// a group story, or a DM with the story's author for a distribution list story.
func (s *SignalClient) sendToStoryAuthor(ctx context.Context, meta *signalid.MessageMetadata, content *signalpb.Content) error {
	if meta.StoryGroupID != "" {
		_, err := s.Client.SendGroupMessage(ctx, types.GroupIdentifier(meta.StoryGroupID), content)
		return err
	}
	authorACI, err := uuid.Parse(meta.StoryAuthor)
	if err != nil {
		return fmt.Errorf("failed to parse story author: %w", err)
	} else if authorACI == s.Client.Store.ACI {
		return ErrOwnStory
	}
	res := s.Client.SendMessage(ctx, libsignalgo.NewACIServiceID(authorACI), content)
	if !res.WasSuccessful {
		return res.Error
	}
	return nil
}

// handleMatrixStoriesMessage handles a message sent to the stories room: a reply to a bridged story
// if it's a Matrix reply, otherwise a new story posted to "My Story".
func (s *SignalClient) handleMatrixStoriesMessage(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if msg.ReplyTo != nil {
		return s.sendStoryReply(ctx, msg)
	}
	return s.postStory(ctx, msg)
}

func (s *SignalClient) sendStoryReply(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	meta, err := storyMetadata(msg.ReplyTo)
	if err != nil {
		return nil, err
	} else if !meta.StoryAllowsReplies {
		return nil, ErrStoryNoReplies
	}
	storyContext, err := storyContextFor(meta)
	if err != nil {
		return nil, err
	}
	// Pass a nil replyTo so the converter doesn't add a quote: Signal represents story replies with
	// storyContext instead, and a quote pointing at a story would be dangling.
	converted, err := s.Main.MsgConv.ToSignal(ctx, s.Client, msg.Portal, msg.Event, msg.Content, msg.OrigSender != nil, nil)
	if err != nil {
		return nil, err
	}
	converted.StoryContext = storyContext

	ts := getTimestampForEvent(msg.InputTransactionID, msg.Event, msg.OrigSender)
	converted.Timestamp = &ts
	msgID := signalid.MakeMessageID(s.Client.Store.ACI, ts)
	msg.AddPendingToIgnore(networkid.TransactionID(msgID))
	err = s.sendToStoryAuthor(ctx, meta, signalmeow.WrapDataMessage(converted))
	if err != nil {
		return nil, bridgev2.WrapErrorInStatus(err).WithSendNotice(true)
	}
	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        msgID,
			SenderID:  signalid.MakeUserID(s.Client.Store.ACI),
			Timestamp: time.UnixMilli(int64(ts)),
			Metadata: &signalid.MessageMetadata{
				ContainsAttachments: len(converted.Attachments) > 0,
			},
		},
		RemovePending: networkid.TransactionID(msgID),
	}, nil
}

func (s *SignalClient) postStory(ctx context.Context, msg *bridgev2.MatrixMessage) (*bridgev2.MatrixMessageResponse, error) {
	if s.Main.Config.DisableStorySend {
		return nil, ErrStorySendDisabled
	}
	story, err := s.Main.MsgConv.StoryToSignal(ctx, s.Client, msg.Portal, msg.Event, msg.Content)
	if err != nil {
		return nil, err
	}
	recipients, err := s.Client.ResolveStoryRecipients(ctx, signalmeow.MyStoryID)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve story recipients: %w", err)
	}

	ts := getTimestampForEvent(msg.InputTransactionID, msg.Event, msg.OrigSender)
	msgID := signalid.MakeMessageID(s.Client.Store.ACI, ts)
	msg.AddPendingToIgnore(networkid.TransactionID(msgID))
	_, err = s.Client.SendStory(ctx, story, signalmeow.MyStoryID, recipients, ts)
	if err != nil {
		return nil, bridgev2.WrapErrorInStatus(err).WithSendNotice(true)
	}
	return &bridgev2.MatrixMessageResponse{
		DB: &database.Message{
			ID:        msgID,
			SenderID:  signalid.MakeUserID(s.Client.Store.ACI),
			Timestamp: time.UnixMilli(int64(ts)),
			Metadata: &signalid.MessageMetadata{
				ContainsAttachments: story.GetFileAttachment() != nil,
				StoryAuthor:         s.Client.Store.ACI.String(),
				StorySentTimestamp:  ts,
				StoryAllowsReplies:  story.GetAllowsReplies(),
				StoryRecipients:     serviceIDsToStrings(recipients),
			},
		},
		RemovePending: networkid.TransactionID(msgID),
	}, nil
}

// sendStoryReaction sends a reaction to a story. Signal delivers these to the story's author (or
// group) as a normal reaction carrying a storyContext.
func (s *SignalClient) sendStoryReaction(
	ctx context.Context,
	target *database.Message,
	emoji string,
	remove bool,
	ts uint64,
) error {
	meta, err := storyMetadata(target)
	if err != nil {
		return err
	}
	storyContext, err := storyContextFor(meta)
	if err != nil {
		return err
	}
	targetAuthorACI, targetSentTimestamp, err := signalid.ParseMessageID(target.ID)
	if err != nil {
		return fmt.Errorf("failed to parse target message ID: %w", err)
	}
	return s.sendToStoryAuthor(ctx, meta, signalmeow.WrapDataMessage(&signalpb.DataMessage{
		Timestamp:               proto.Uint64(ts),
		RequiredProtocolVersion: proto.Uint32(uint32(signalpb.DataMessage_REACTIONS)),
		StoryContext:            storyContext,
		Reaction: &signalpb.DataMessage_Reaction{
			Emoji:                 proto.String(emoji),
			Remove:                proto.Bool(remove),
			TargetAuthorAciBinary: targetAuthorACI[:],
			TargetSentTimestamp:   proto.Uint64(targetSentTimestamp),
		},
	}))
}

func serviceIDsToStrings(serviceIDs []libsignalgo.ServiceID) []string {
	out := make([]string, len(serviceIDs))
	for i, serviceID := range serviceIDs {
		out[i] = serviceID.String()
	}
	return out
}

// storyDeleteRecipients works out who to tell about a deleted story: the recorded recipients if we
// have them, otherwise a fresh resolve of My Story.
func (s *SignalClient) storyDeleteRecipients(ctx context.Context, meta *signalid.MessageMetadata) ([]libsignalgo.ServiceID, error) {
	if len(meta.StoryRecipients) > 0 {
		recipients := make([]libsignalgo.ServiceID, 0, len(meta.StoryRecipients))
		for _, raw := range meta.StoryRecipients {
			serviceID, err := libsignalgo.ServiceIDFromString(raw)
			if err != nil {
				zerolog.Ctx(ctx).Warn().Err(err).Str("raw_service_id", raw).
					Msg("Failed to parse stored story recipient")
				continue
			}
			recipients = append(recipients, serviceID)
		}
		if len(recipients) > 0 {
			return recipients, nil
		}
	}
	return s.Client.ResolveStoryRecipients(ctx, signalmeow.MyStoryID)
}

// deleteStory removes a story we posted. Signal does this by sending a normal delete-for-everyone
// data message to each story recipient individually with story=true, rather than as a
// multi-recipient send (see sendDeleteStoryForEveryone in Signal Desktop).
func (s *SignalClient) deleteStory(ctx context.Context, target *database.Message, ts uint64) error {
	meta, err := storyMetadata(target)
	if err != nil {
		return err
	}
	_, targetSentTimestamp, err := signalid.ParseMessageID(target.ID)
	if err != nil {
		return fmt.Errorf("failed to parse target message ID: %w", err)
	}
	if meta.StoryGroupID != "" {
		_, err = s.Client.SendGroupMessage(ctx, types.GroupIdentifier(meta.StoryGroupID),
			signalmeow.WrapDataMessage(storyDeleteMessage(s.Client.Store.ACI, targetSentTimestamp, ts)))
		return err
	}
	recipients, err := s.storyDeleteRecipients(ctx, meta)
	if err != nil {
		return fmt.Errorf("failed to resolve story delete recipients: %w", err)
	} else if len(recipients) == 0 {
		return fmt.Errorf("no recipients to send story deletion to")
	}
	log := zerolog.Ctx(ctx)
	var lastErr error
	var successes int
	for _, recipient := range recipients {
		if recipient.Type == libsignalgo.ServiceIDTypeACI && recipient.UUID == s.Client.Store.ACI {
			continue
		}
		res := s.Client.SendMessage(ctx, recipient,
			signalmeow.WrapDataMessage(storyDeleteMessage(s.Client.Store.ACI, targetSentTimestamp, ts)))
		if !res.WasSuccessful {
			lastErr = res.Error
			log.Warn().Err(res.Error).Stringer("recipient", recipient).
				Msg("Failed to send story deletion to recipient")
		} else {
			successes++
		}
	}
	if successes == 0 && lastErr != nil {
		return fmt.Errorf("failed to send story deletion to any recipient: %w", lastErr)
	}
	return nil
}

func storyDeleteMessage(ourACI uuid.UUID, targetSentTimestamp, ts uint64) *signalpb.DataMessage {
	return &signalpb.DataMessage{
		Timestamp: proto.Uint64(ts),
		Delete: &signalpb.DataMessage_Delete{
			TargetSentTimestamp: proto.Uint64(targetSentTimestamp),
		},
		StoryContext: &signalpb.DataMessage_StoryContext{
			AuthorAciBinary: ourACI[:],
			SentTimestamp:   proto.Uint64(targetSentTimestamp),
		},
	}
}
