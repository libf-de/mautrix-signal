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
	"html"
	"strings"

	"github.com/google/uuid"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-signal/pkg/msgconv/signalfmt"
	"go.mau.fi/mautrix-signal/pkg/signalid"
	"go.mau.fi/mautrix-signal/pkg/signalmeow"
	signalpb "go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf"
)

// TextStoryEventKey holds the raw Signal text story styling that Matrix can't represent.
const TextStoryEventKey = "fi.mau.signal.text_story"

// StoryInfo is the extra context needed to convert a story, which doesn't live on the
// StoryMessage proto itself.
type StoryInfo struct {
	// Timestamp is the story's sent timestamp (from the envelope).
	Timestamp uint64
	// GroupID is empty for stories sent to a distribution list.
	GroupID string
	// GroupName is used to label group stories. Empty if unknown or not a group story.
	GroupName string
}

// argbToCSS converts Signal's integer hex colors (0xAARRGGBB) to a CSS color string.
// The alpha channel is dropped since Matrix's data-mx-color doesn't support it.
func argbToCSS(color uint32) string {
	return fmt.Sprintf("#%06x", color&0xFFFFFF)
}

// storyBackgroundColor picks a single representative color for a text story's background.
// Matrix HTML has no gradient support, so the first gradient stop is used.
func storyBackgroundColor(ta *signalpb.TextAttachment) (uint32, bool) {
	switch bg := ta.GetBackground().(type) {
	case *signalpb.TextAttachment_Color:
		return bg.Color, true
	case *signalpb.TextAttachment_Gradient_:
		if colors := bg.Gradient.GetColors(); len(colors) > 0 {
			return colors[0], true
		}
		// startColor is deprecated but still sent by older clients.
		if bg.Gradient.StartColor != nil {
			return bg.Gradient.GetStartColor(), true
		}
	}
	return 0, false
}

func (mc *MessageConverter) StoryToMatrix(
	ctx context.Context,
	client *signalmeow.Client,
	portal *bridgev2.Portal,
	sender uuid.UUID,
	intent bridgev2.MatrixAPI,
	story *signalpb.StoryMessage,
	info StoryInfo,
) *bridgev2.ConvertedMessage {
	ctx = context.WithValue(ctx, contextKeyClient, client)
	ctx = context.WithValue(ctx, contextKeyPortal, portal)
	ctx = context.WithValue(ctx, contextKeyIntent, intent)
	cm := &bridgev2.ConvertedMessage{
		Parts: make([]*bridgev2.ConvertedMessagePart, 0, 1),
	}

	switch att := story.GetAttachment().(type) {
	case *signalpb.StoryMessage_FileAttachment:
		cm.Parts = append(cm.Parts, mc.convertAttachmentToMatrix(ctx, 0, att.FileAttachment, nil))
	case *signalpb.StoryMessage_TextAttachment:
		cm.Parts = append(cm.Parts, mc.convertTextStoryToMatrix(ctx, story, att.TextAttachment))
	default:
		cm.Parts = append(cm.Parts, &bridgev2.ConvertedMessagePart{
			Type: event.EventMessage,
			Content: &event.MessageEventContent{
				MsgType: event.MsgNotice,
				Body:    "Unsupported story type",
			},
		})
	}

	if info.GroupName != "" {
		addStoryGroupLabel(cm.Parts[0], info.GroupName)
	}

	meta := &signalid.MessageMetadata{
		ContainsAttachments: story.GetFileAttachment() != nil,
		StoryAuthor:         sender.String(),
		StorySentTimestamp:  info.Timestamp,
		StoryGroupID:        info.GroupID,
		StoryAllowsReplies:  story.GetAllowsReplies(),
	}
	for i, part := range cm.Parts {
		part.ID = signalid.MakeMessagePartID(i)
		part.DBMetadata = meta
	}
	return cm
}

func (mc *MessageConverter) convertTextStoryToMatrix(
	ctx context.Context,
	story *signalpb.StoryMessage,
	ta *signalpb.TextAttachment,
) *bridgev2.ConvertedMessagePart {
	content := signalfmt.Parse(ctx, ta.GetText(), story.GetBodyRanges(), mc.SignalFmtParams)
	if len(ta.GetPreview().GetUrl()) > 0 {
		content.BeeperLinkPreviews = mc.convertURLPreviewsToBeeper(ctx, []*signalpb.Preview{ta.GetPreview()}, nil)
	}

	rawStyle := map[string]any{
		"style": signalpb.TextAttachment_Style_name[int32(ta.GetTextStyle())],
	}
	var styles []string
	if bgColor, ok := storyBackgroundColor(ta); ok {
		styles = append(styles, fmt.Sprintf(`data-mx-bg-color="%s"`, argbToCSS(bgColor)))
		rawStyle["background_color"] = argbToCSS(bgColor)
	}
	if ta.TextForegroundColor != nil {
		styles = append(styles, fmt.Sprintf(`data-mx-color="%s"`, argbToCSS(ta.GetTextForegroundColor())))
		rawStyle["text_foreground_color"] = argbToCSS(ta.GetTextForegroundColor())
	}
	if ta.TextBackgroundColor != nil {
		rawStyle["text_background_color"] = argbToCSS(ta.GetTextBackgroundColor())
	}
	if gradient := ta.GetGradient(); gradient != nil {
		rawGradient := map[string]any{"angle": gradient.GetAngle()}
		colors := make([]string, len(gradient.GetColors()))
		for i, color := range gradient.GetColors() {
			colors[i] = argbToCSS(color)
		}
		rawGradient["colors"] = colors
		rawGradient["positions"] = gradient.GetPositions()
		rawStyle["gradient"] = rawGradient
	}

	if len(styles) > 0 {
		formatted := content.FormattedBody
		if content.Format != event.FormatHTML {
			formatted = event.TextToHTML(content.Body)
		}
		content.Format = event.FormatHTML
		content.FormattedBody = fmt.Sprintf("<span %s>%s</span>", strings.Join(styles, " "), formatted)
	}

	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: content,
		Extra:   map[string]any{TextStoryEventKey: rawStyle},
	}
}

// addStoryGroupLabel prefixes a converted story part with the name of the group it was sent to.
func addStoryGroupLabel(part *bridgev2.ConvertedMessagePart, groupName string) {
	content := part.Content
	if content == nil {
		return
	}
	if content.Format == event.FormatHTML && content.FormattedBody != "" {
		content.FormattedBody = fmt.Sprintf("<b>%s</b><br>%s", html.EscapeString(groupName), content.FormattedBody)
	} else if content.Body != "" {
		content.Format = event.FormatHTML
		content.FormattedBody = fmt.Sprintf("<b>%s</b><br>%s", html.EscapeString(groupName), event.TextToHTML(content.Body))
	}
	if content.Body != "" {
		content.Body = fmt.Sprintf("%s\n%s", groupName, content.Body)
	} else {
		content.Body = groupName
	}
}
