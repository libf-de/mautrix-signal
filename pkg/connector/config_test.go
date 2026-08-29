package connector

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	up "go.mau.fi/util/configupgrade"
	"gopkg.in/yaml.v3"
)

// TestUpgradeConfigPreservesValues checks that the story options are listed in upgradeConfig.
// A missing or misspelled helper.Copy silently resets that option on every start.
func TestUpgradeConfigPreservesValues(t *testing.T) {
	var base yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(ExampleConfig), &base))

	modified := strings.NewReplacer(
		"enable_stories: false", "enable_stories: true",
		"disable_story_send: true", "disable_story_send: false",
		"mute_stories: true", "mute_stories: false",
		"stories_tag: m.lowpriority", "stories_tag: m.favourite",
		"use_contact_avatars: false", "use_contact_avatars: true",
	).Replace(ExampleConfig)

	var existing yaml.Node
	require.NoError(t, yaml.Unmarshal([]byte(modified), &existing))

	helper := up.NewHelper(&base, &existing)
	up.SimpleUpgrader(upgradeConfig).DoUpgrade(helper)

	upgraded, err := yaml.Marshal(&base)
	require.NoError(t, err)

	var cfg SignalConfig
	require.NoError(t, yaml.Unmarshal(upgraded, &cfg))

	assert.True(t, cfg.EnableStories, "enable_stories should survive the config upgrade")
	assert.False(t, cfg.DisableStorySend, "disable_story_send should survive the config upgrade")
	assert.False(t, cfg.MuteStories, "mute_stories should survive the config upgrade")
	assert.EqualValues(t, "m.favourite", cfg.StoriesTag, "stories_tag should survive the config upgrade")
	assert.True(t, cfg.UseContactAvatars, "control: a pre-existing option should also survive")
}
