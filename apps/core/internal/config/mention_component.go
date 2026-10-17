package config

import (
	"github.com/ivannguyendev/chatim/apps/mention/mentionapp"
	"github.com/ivannguyendev/chatim/pkg/envconfig"
)

func (p *parser) mentionConfig(c *Config) {
	m := mentionapp.DefaultConfig()
	m.Enabled = p.flag("MENTION_ENABLED", true)
	m.EventStream, m.EventSubjectRoot, m.LiveRoot, m.Replicas = c.Stream.Name, c.Stream.SubjectRoot, c.Stream.LiveRoot, c.Stream.Replicas
	m.FetchBatch = p.count("MENTION_FETCH_BATCH", m.FetchBatch)
	m.FetchWait = p.span("MENTION_FETCH_WAIT", m.FetchWait)
	m.Workers = p.count("MENTION_WORKERS", m.Workers)
	m.MaxAckPending = p.count("MENTION_MAX_ACK_PENDING", m.MaxAckPending)
	m.NotifyMaxAge = p.span("MENTION_NOTIFY_MAX_AGE", m.NotifyMaxAge)
	m.NotifyStream = envconfig.String("NOTIFY_STREAM", m.NotifyStream)
	m.NotifySubjectRoot = envconfig.String("NOTIFY_SUBJECT_ROOT", m.NotifySubjectRoot)
	m.NotifyMaxStreamAge = p.span("NOTIFY_STREAM_MAX_AGE", m.NotifyMaxStreamAge)
	m.NotifyDuplicates = p.span("NOTIFY_STREAM_DUPLICATES", m.NotifyDuplicates)
	m.HideReadTenants = c.MentionHideReadTenants
	m.RoomGroupsSyncMax = p.count("MENTION_ROOM_GROUPS_SYNC_MAX", m.RoomGroupsSyncMax)
	m.RoomGroupsTTL = p.span("MENTION_ROOM_GROUPS_TTL", m.RoomGroupsTTL)
	m.RoomGroupsRebuild = p.span("MENTION_ROOM_GROUPS_REBUILD", m.RoomGroupsRebuild)
	c.Mention = m
}

func (c Config) mentionRules() []rule {
	m := c.Mention
	if !m.Enabled {
		return nil
	}
	roots := []string{c.Stream.SubjectRoot, c.Stream.LiveRoot, c.Timer.SubjectRoot}
	distinctRoot := true
	for _, root := range roots {
		distinctRoot = distinctRoot && m.NotifySubjectRoot != root
	}
	return []rule{
		{m.NotifyStream != c.Stream.Name && m.NotifyStream != c.Timer.Name, "NOTIFY_STREAM must differ from EVT_STREAM and TIMER_STREAM"},
		{distinctRoot, "NOTIFY_SUBJECT_ROOT must differ from EVT_SUBJECT_ROOT, EVT_LIVE_ROOT and TIMER_SUBJECT_ROOT"},
		{m.FetchWait <= c.Projection.FetchWait, "MENTION_FETCH_WAIT must not exceed PROJECTION_FETCH_WAIT"},
	}
}

func (c Config) mentionErrors() error {
	if !c.Mention.Enabled {
		return nil
	}
	return c.Mention.Validate()
}
