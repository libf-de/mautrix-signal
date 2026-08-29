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

package msgconv

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-signal/pkg/msgconv/matrixfmt"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	signalpb "go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf"
)

// defaultTextStoryBackground is used for text stories posted from Matrix. Signal requires text
// stories to have a background, and Matrix has no way to pick one.
const defaultTextStoryBackground uint32 = 0xff2c6bed

// StoryToSignal converts a Matrix message into a Signal story.
func (mc *MessageConverter) StoryToSignal(
	ctx context.Context,
	client *signalmeow.Client,
	portal *bridgev2.Portal,
	evt *event.Event,
	content *event.MessageEventContent,
) (*signalpb.StoryMessage, error) {
	ctx = context.WithValue(ctx, contextKeyClient, client)
	ctx = context.WithValue(ctx, contextKeyPortal, portal)

	story := &signalpb.StoryMessage{
		AllowsReplies: proto.Bool(true),
	}
	if profileKey, err := client.ProfileKeyForSignalID(ctx, client.Store.ACI); err != nil {
		return nil, fmt.Errorf("failed to get own profile key: %w", err)
	} else if profileKey != nil {
		story.ProfileKey = profileKey.Slice()
	}

	body, bodyRanges := matrixfmt.Parse(ctx, mc.MatrixFmtParams, content)
	switch content.MsgType {
	case event.MsgText, event.MsgNotice, event.MsgEmote:
		if body == "" {
			return nil, fmt.Errorf("%w: text stories can't be empty", bridgev2.ErrUnsupportedMessageType)
		}
		story.BodyRanges = bodyRanges
		story.Attachment = &signalpb.StoryMessage_TextAttachment{
			TextAttachment: &signalpb.TextAttachment{
				Text:       proto.String(body),
				TextStyle:  signalpb.TextAttachment_REGULAR.Enum(),
				Background: &signalpb.TextAttachment_Color{Color: defaultTextStoryBackground},
			},
		}
	case event.MsgImage, event.MsgVideo:
		att, err := mc.convertFileToSignal(ctx, evt, content)
		if err != nil {
			return nil, fmt.Errorf("failed to convert story attachment: %w", err)
		}
		story.Attachment = &signalpb.StoryMessage_FileAttachment{FileAttachment: att}
	default:
		return nil, fmt.Errorf("%w %s as a story", bridgev2.ErrUnsupportedMessageType, content.MsgType)
	}
	return story, nil
}
