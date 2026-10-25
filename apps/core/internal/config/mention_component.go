package config

import (
	"slices"
	"strings"

	"github.com/ivannguyendev/chatim/modules/mention/mentionapp"
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
	m.WsPublishStream = envconfig.String("MENTION_WS_PUBLISH_STREAM", m.WsPublishStream)
	m.WsPublishRoot = envconfig.String("MENTION_WS_PUBLISH_ROOT", m.WsPublishRoot)
	m.TargetsPage = p.count("MENTION_NOTIFY_TARGETS_PAGE", m.TargetsPage)
	m.HideReadTenants = p.tenants("MENTION_HIDE_READ_TENANTS")
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
	first, _, _ := strings.Cut(m.WsPublishRoot, ".")
	distinctRoot := !slices.Contains([]string{c.Stream.SubjectRoot, c.Stream.LiveRoot, c.Timer.SubjectRoot}, first)
	return []rule{
		{m.WsPublishStream != c.Stream.Name && m.WsPublishStream != c.Timer.Name, "MENTION_WS_PUBLISH_STREAM must differ from EVT_STREAM and TIMER_STREAM"},
		{distinctRoot, "MENTION_WS_PUBLISH_ROOT must not start with EVT_SUBJECT_ROOT, EVT_LIVE_ROOT or TIMER_SUBJECT_ROOT"},
		{m.FetchWait <= c.Projection.FetchWait, "MENTION_FETCH_WAIT must not exceed PROJECTION_FETCH_WAIT"},
	}
}

func (c Config) mentionErrors() error {
	if !c.Mention.Enabled {
		return nil
	}
	return c.Mention.Validate()
}
