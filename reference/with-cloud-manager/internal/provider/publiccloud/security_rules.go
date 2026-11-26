package publiccloud

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	"with-cloud-manager/internal/provider"
)

type ruleWalk struct {
	GroupPage string `json:"group_page,omitempty"`
	GroupID   string `json:"group_id,omitempty"`
	RulePage  string `json:"rule_page,omitempty"`
}

func validSecurityGroupID(id string) bool {
	return strings.HasPrefix(id, "sg-") && len(id) > len("sg-") && resourceID.MatchString(id)
}

func validRuleDirection(direction string) bool {
	return direction == "ingress" || direction == "egress"
}

func walkSecurityRules(ctx context.Context, region, cursor string,
	groups func(context.Context, string, string, int) (provider.Page, error),
	rules func(context.Context, string, string, string) (provider.Page, error),
) (provider.Page, error) {
	var state ruleWalk
	if cursor != "" {
		if err := json.Unmarshal([]byte(cursor), &state); err != nil ||
			(state.GroupID == "" && (state.RulePage != "" || state.GroupPage == "")) ||
			(state.GroupID != "" && (!validSecurityGroupID(state.GroupID) || state.RulePage == "")) {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
	}

	groupsPage, err := groups(ctx, region, state.GroupPage, 1)
	if err != nil {
		return provider.Page{}, err
	}
	if err := groupsPage.Validate(state.GroupPage); err != nil {
		return provider.Page{}, err
	}
	if len(groupsPage.Objects) == 0 {
		if state.GroupID != "" {
			return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
		}
		return provider.Page{}, nil
	}
	id := groupsPage.Objects[0].ID
	if state.GroupID != "" && state.GroupID != id {
		return provider.Page{}, &provider.Failure{Kind: provider.ExpiredCursor}
	}

	page, err := rules(ctx, region, id, state.RulePage)
	if err != nil {
		return provider.Page{}, err
	}
	if err := page.Validate(state.RulePage); err != nil {
		return provider.Page{}, err
	}
	var next ruleWalk
	switch {
	case page.Next != "":
		next = ruleWalk{GroupPage: state.GroupPage, GroupID: id, RulePage: page.Next}
	case groupsPage.Next != "":
		next = ruleWalk{GroupPage: groupsPage.Next}
	default:
		return page, nil
	}
	encoded, err := json.Marshal(next)
	if err != nil || len(encoded) > provider.MaxCursorBytes {
		return provider.Page{}, &provider.Failure{Kind: provider.InvalidResponse}
	}
	page.Next = string(encoded)
	return page, nil
}

func ruleDetail(name, value string) string {
	if value == "" {
		return ""
	}
	return " " + name + "=" + value
}

func rulePriority(priority *int) string {
	if priority == nil {
		return ""
	}
	return ruleDetail("priority", strconv.Itoa(*priority))
}
