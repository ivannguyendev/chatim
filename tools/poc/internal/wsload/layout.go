package wsload

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

type Layout struct {
	Users, Groups, Nodes int
}

func (l Layout) Validate(maxMembers int) error {
	switch {
	case l.Users <= 0 || l.Groups <= 0 || l.Nodes <= 0:
		return errors.New("users, groups and nodes must be positive")
	case l.Groups > l.Users:
		return fmt.Errorf("groups %d must not exceed users %d, or a group has no member", l.Groups, l.Users)
	case l.MembersOf(0) > maxMembers:
		return fmt.Errorf("users/groups gives %d members per group, more than %d", l.MembersOf(0), maxMembers)
	}
	return nil
}

func (l Layout) GroupOf(user int) int { return user % l.Groups }

func (l Layout) NodeOf(user int) int { return (user / l.Groups) % l.Nodes }

func (l Layout) MembersOf(group int) int { return (l.Users - group + l.Groups - 1) / l.Groups }

func (l Layout) Members(group int) []int {
	out := make([]int, 0, l.MembersOf(group))
	for user := group; user < l.Users; user += l.Groups {
		out = append(out, user)
	}
	return out
}

func (l Layout) SenderOf(group int) int {
	return group + (group%l.MembersOf(group))*l.Groups
}

func StampText(run string, sent time.Time) string {
	return run + " " + strconv.FormatInt(sent.UnixNano(), 10)
}

func ParseStamp(run, text string) (time.Time, bool) {
	rest, ok := strings.CutPrefix(text, run+" ")
	if !ok {
		return time.Time{}, false
	}
	nanos, err := strconv.ParseInt(rest, 10, 64)
	if err != nil || nanos <= 0 {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}
