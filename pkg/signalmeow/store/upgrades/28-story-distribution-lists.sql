-- v28 (compatible with v13+): Store story distribution lists from the storage service
CREATE TABLE signalmeow_story_distribution_list (
    account_id      TEXT    NOT NULL,
    distribution_id TEXT    NOT NULL,
    name            TEXT    NOT NULL,
    allows_replies  BOOLEAN NOT NULL,
    is_block_list   BOOLEAN NOT NULL,
    recipients      jsonb   NOT NULL,
    deleted_at_ts   BIGINT  NOT NULL,

    PRIMARY KEY (account_id, distribution_id),
    CONSTRAINT signalmeow_story_distribution_list_device_fkey
        FOREIGN KEY (account_id) REFERENCES signalmeow_device (aci_uuid) ON DELETE CASCADE ON UPDATE CASCADE
);
