package msgconv

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"maunium.net/go/mautrix/bridgev2"
	"maunium.net/go/mautrix/event"

	"go.mau.fi/mautrix-signal/pkg/msgconv/signalfmt"
	signalpb "go.mau.fi/mautrix-signal/pkg/signalmeow/protobuf"
)

func newTestPart(body string) *bridgev2.ConvertedMessagePart {
	return &bridgev2.ConvertedMessagePart{
		Type:    event.EventMessage,
		Content: &event.MessageEventContent{MsgType: event.MsgText, Body: body},
	}
}

func testConverter() *MessageConverter {
	return &MessageConverter{SignalFmtParams: &signalfmt.FormatParams{}}
}

func TestArgbToCSS(t *testing.T) {
	assert.Equal(t, "#ff0000", argbToCSS(0xffff0000))
	assert.Equal(t, "#2c6bed", argbToCSS(0xff2c6bed))
	// Alpha is dropped, not merged into the color.
	assert.Equal(t, "#000000", argbToCSS(0xff000000))
}

func TestStoryBackgroundColor(t *testing.T) {
	solid := &signalpb.TextAttachment{
		Background: &signalpb.TextAttachment_Color{Color: 0xff112233},
	}
	color, ok := storyBackgroundColor(solid)
	require.True(t, ok)
	assert.Equal(t, uint32(0xff112233), color)

	gradient := &signalpb.TextAttachment{
		Background: &signalpb.TextAttachment_Gradient_{Gradient: &signalpb.TextAttachment_Gradient{
			Colors: []uint32{0xffaabbcc, 0xff445566},
			Angle:  proto.Uint32(180),
		}},
	}
	color, ok = storyBackgroundColor(gradient)
	require.True(t, ok, "the first gradient stop should be used")
	assert.Equal(t, uint32(0xffaabbcc), color)

	// Older clients only send the deprecated startColor.
	legacy := &signalpb.TextAttachment{
		Background: &signalpb.TextAttachment_Gradient_{Gradient: &signalpb.TextAttachment_Gradient{
			StartColor: proto.Uint32(0xff778899),
		}},
	}
	color, ok = storyBackgroundColor(legacy)
	require.True(t, ok)
	assert.Equal(t, uint32(0xff778899), color)

	_, ok = storyBackgroundColor(&signalpb.TextAttachment{})
	assert.False(t, ok)
}

func TestConvertTextStoryToMatrix(t *testing.T) {
	mc := testConverter()
	story := &signalpb.StoryMessage{}
	part := mc.convertTextStoryToMatrix(context.Background(), story, &signalpb.TextAttachment{
		Text:                proto.String("hello stories"),
		TextStyle:           signalpb.TextAttachment_BOLD.Enum(),
		TextForegroundColor: proto.Uint32(0xffffffff),
		Background: &signalpb.TextAttachment_Gradient_{Gradient: &signalpb.TextAttachment_Gradient{
			Colors:    []uint32{0xff2c6bed, 0xff112233},
			Angle:     proto.Uint32(90),
			Positions: []float32{0, 1},
		}},
	})

	assert.Equal(t, "hello stories", part.Content.Body)
	assert.Equal(t, event.FormatHTML, part.Content.Format)
	assert.Contains(t, part.Content.FormattedBody, `data-mx-bg-color="#2c6bed"`)
	assert.Contains(t, part.Content.FormattedBody, `data-mx-color="#ffffff"`)
	assert.Contains(t, part.Content.FormattedBody, "hello stories")

	raw, ok := part.Extra[TextStoryEventKey].(map[string]any)
	require.True(t, ok, "raw text story styling should be in the extra content")
	assert.Equal(t, "BOLD", raw["style"])
	assert.Equal(t, "#2c6bed", raw["background_color"])
	gradient, ok := raw["gradient"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, []string{"#2c6bed", "#112233"}, gradient["colors"])
	assert.Equal(t, uint32(90), gradient["angle"])
}

func TestConvertTextStoryToMatrixNoStyling(t *testing.T) {
	mc := testConverter()
	part := mc.convertTextStoryToMatrix(context.Background(), &signalpb.StoryMessage{}, &signalpb.TextAttachment{
		Text: proto.String("plain"),
	})
	assert.Equal(t, "plain", part.Content.Body)
	// No colors means no wrapping span, so the message stays unformatted.
	assert.NotContains(t, part.Content.FormattedBody, "data-mx-bg-color")
}

func TestAddStoryGroupLabel(t *testing.T) {
	t.Run("plaintext", func(t *testing.T) {
		part := newTestPart("just text")
		addStoryGroupLabel(part, "Cool Group")
		assert.Equal(t, "Cool Group\njust text", part.Content.Body)
		assert.Equal(t, event.FormatHTML, part.Content.Format)
		assert.Contains(t, part.Content.FormattedBody, "<b>Cool Group</b><br>")
		assert.Contains(t, part.Content.FormattedBody, "just text")
	})
	t.Run("escapes group name", func(t *testing.T) {
		part := newTestPart("body")
		addStoryGroupLabel(part, "<script>")
		assert.Contains(t, part.Content.FormattedBody, "&lt;script&gt;")
		assert.NotContains(t, part.Content.FormattedBody, "<script>")
	})
	t.Run("keeps existing html", func(t *testing.T) {
		part := newTestPart("bold")
		part.Content.Format = event.FormatHTML
		part.Content.FormattedBody = "<b>bold</b>"
		addStoryGroupLabel(part, "Group")
		assert.Equal(t, "<b>Group</b><br><b>bold</b>", part.Content.FormattedBody)
	})
	t.Run("attachment with no body", func(t *testing.T) {
		part := newTestPart("")
		addStoryGroupLabel(part, "Group")
		assert.Equal(t, "Group", part.Content.Body)
	})
}
