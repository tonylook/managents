// Package demo provides canned display scenes, to try a display without any
// agent running and to check every layout on real hardware.
package demo

import (
	"fmt"
	"time"

	"github.com/tonylook/managents/helper/internal/agent"
)

// Scene is one demo screen.
type Scene struct {
	Title string
	Cards []agent.Card
}

func sessionsOf(cards []agent.Card) []agent.Session {
	sessions := make([]agent.Session, len(cards))
	for i, c := range cards {
		sessions[i] = c.Session
	}
	return sessions
}

// Scenes returns the demo sequence; ages are relative to now.
func Scenes(now time.Time) []Scene {
	ago := func(d time.Duration) time.Time { return now.Add(-d) }
	card := func(id int, kind agent.Kind, name string, status agent.Status, age time.Duration) agent.Card {
		return agent.Card{
			Name: name,
			Session: agent.Session{
				ID: fmt.Sprintf("%s:%d", kind, id), Kind: kind, Dir: "/demo/" + name,
				Status: status, Since: ago(age),
			},
		}
	}
	withContext := func(c agent.Card, used, limit int) agent.Card {
		c.Context = &agent.ContextUsage{Used: used, Limit: limit}
		return c
	}

	mixed := []agent.Card{
		withContext(card(1, agent.KindClaude, "managents", agent.StatusWorking, 12*time.Second), 88000, 200000),
		card(2, agent.KindClaude, "agent-lights", agent.StatusWaiting, 5*time.Minute),
		card(3, agent.KindOpenCode, "integrationlab", agent.StatusError, 45*time.Second),
		withContext(card(4, agent.KindClaude, "staticdata-write-api", agent.StatusIdle, 150*time.Minute), 240000, 1000000),
	}

	statuses := []agent.Status{agent.StatusWorking, agent.StatusWaiting, agent.StatusIdle, agent.StatusWorking}
	var many []agent.Card
	for i, name := range []string{"api", "web", "core", "infra", "docs", "mobile", "billing", "search",
		"auth", "etl", "ml-models", "design-system", "sandbox", "notes"} {
		kind := agent.KindClaude
		if i%3 == 0 {
			kind = agent.KindOpenCode
		}
		many = append(many, card(100+i, kind, name, statuses[i%len(statuses)], time.Duration(i*97)*time.Second))
	}

	return []Scene{
		{Title: "one agent working", Cards: mixed[:1]},
		{Title: "two agents", Cards: mixed[:2]},
		{Title: "four mixed statuses", Cards: mixed},
		{Title: "context almost full", Cards: []agent.Card{
			withContext(card(5, agent.KindClaude, "refactor", agent.StatusWorking, 3*time.Minute), 150000, 200000),
			withContext(card(6, agent.KindClaude, "migration", agent.StatusWaiting, 20*time.Second), 188000, 200000),
		}},
		{Title: "fourteen agents: tap the screen for page two", Cards: agent.Arrange(sessionsOf(many))},
		{Title: "no agents", Cards: nil},
	}
}
