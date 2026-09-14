package alerts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Delivery targets that are not a herdr pane or agent name.
const (
	// TargetYou raises a desktop notification instead of waking an agent.
	TargetYou = "you"
	// TargetRepo finds whichever agent is already working in the checkout the
	// alarm concerns. The agent sitting in the repository is the one that
	// should hear that its pull request went red.
	TargetRepo = "repo"
)

// blockedRetry is how long to keep trying an agent that is busy with a dialog,
// while an agent is blocked on a dialog.
const (
	blockedRetry = 60 * time.Second
	retryEvery   = 5 * time.Second
	herdrTimeout = 15 * time.Second
)

// Agent is one herdr pane running an agent.
type Agent struct {
	PaneID    string `json:"pane_id"`
	Agent     string `json:"agent"`
	Status    string `json:"agent_status"`
	CWD       string `json:"cwd"`
	Title     string `json:"terminal_title_stripped"`
	Workspace string `json:"workspace_id"`
}

// Agents lists the herdr agents currently running, for the delivery picker and
// for repo targeting. An absent herdr is not an error: it just means there is
// nobody to wake but the person at the keyboard.
func Agents(ctx context.Context) ([]Agent, error) {
	ctx, cancel := context.WithTimeout(ctx, herdrTimeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "herdr", "agent", "list").Output()
	if err != nil {
		return nil, fmt.Errorf("herdr agent list: %w", err)
	}

	var reply struct {
		Result struct {
			Agents []Agent `json:"agents"`
		} `json:"result"`
	}
	if err := json.Unmarshal(out, &reply); err != nil {
		return nil, fmt.Errorf("herdr gave an unreadable answer: %w", err)
	}
	return reply.Result.Agents, nil
}

// Notifier raises a desktop notification, so alerts reuse the one call site
// that already knows how.
type Notifier func(urgency, title, body string) error

// NewDeliverer builds the delivery function the engine calls.
func NewDeliverer(desktop Notifier) Deliverer {
	return func(t Trigger, f Fire) (string, error) {
		text := Alarm(t, f)

		switch {
		case t.DeliverTo == TargetYou || t.DeliverTo == "":
			return "desktop", deliverDesktop(desktop, t, f)
		case t.DeliverTo == TargetRepo:
			agents, err := Agents(context.Background())
			if err != nil {
				// No herdr means nobody is working anywhere, which is the same
				// answer as nobody working here.
				return "desktop", deliverDesktop(desktop, t, f)
			}
			target, ok := agentForRepo(agents, f)
			if !ok {
				// Nobody is working there, so tell the person instead of
				// dropping the alarm.
				return "desktop", deliverDesktop(desktop, t, f)
			}
			if err := deliverHerdr(context.Background(), target, text); err != nil {
				return "desktop", deliverDesktop(desktop, t, f)
			}
			return "herdr:" + target, nil
		default:
			if err := deliverHerdr(context.Background(), t.DeliverTo, text); err != nil {
				// A named agent that cannot be reached falls back rather than
				// losing the alarm, and the error is recorded either way.
				if fallback := deliverDesktop(desktop, t, f); fallback != nil {
					return "", errors.Join(err, fallback)
				}
				return "desktop", nil
			}
			return "herdr:" + t.DeliverTo, nil
		}
	}
}

// Alarm is the wake-up text. It names the trigger, the condition and the
// reason, and carries no values: whoever is woken reads the sentinel.
func Alarm(t Trigger, f Fire) string {
	var b strings.Builder
	fmt.Fprintf(&b, "sentinel alarm %s: %s", t.ID, f.Summary)
	fmt.Fprintf(&b, " at %s.", f.At.UTC().Format(time.RFC3339))
	if t.Reason != "" {
		fmt.Fprintf(&b, ` Reason: %q.`, t.Reason)
	}
	fmt.Fprintf(&b, " Armed by %s at %s.", t.ArmedBy, t.ArmedAt.UTC().Format(time.RFC3339))
	b.WriteString(" This message carries no values; read the sentinel yourself with the sentinel_* MCP tools.")
	if t.Standing {
		fmt.Fprintf(&b, " It stays armed until %s; disarm with: sentinel disarm %s.",
			t.ExpiresAt.UTC().Format(time.RFC3339), t.ID)
	} else {
		b.WriteString(" This was a one-shot and is now spent.")
	}
	return b.String()
}

// deliverDesktop raises the alarm with the person at the keyboard.
func deliverDesktop(desktop Notifier, t Trigger, f Fire) error {
	if desktop == nil {
		return errors.New("no desktop notifier configured")
	}
	body := f.Summary
	if t.Reason != "" {
		body += "\n" + t.Reason
	}
	return desktop("critical", "sentinel alarm", body)
}

// deliverHerdr prompts an agent, retrying while it is blocked on a dialog.
func deliverHerdr(ctx context.Context, target, text string) error {
	deadline := time.Now().Add(blockedRetry)
	var last error

	for {
		attempt, cancel := context.WithTimeout(ctx, herdrTimeout)
		cmd := exec.CommandContext(attempt, "herdr", "agent", "prompt", target, text)
		out, err := cmd.CombinedOutput()
		cancel()

		if err == nil {
			return nil
		}
		last = fmt.Errorf("herdr agent prompt %s: %w: %s",
			target, err, strings.TrimSpace(string(out)))

		if time.Now().After(deadline) {
			return last
		}
		select {
		case <-ctx.Done():
			return last
		case <-time.After(retryEvery):
		}
	}
}

// agentForRepo finds the agent whose working directory sits inside the checkout
// the alarm is about. An agent sitting exactly at the checkout counts, and so
// does one further down it; one merely sharing a name prefix does not.
func agentForRepo(agents []Agent, f Fire) (string, bool) {
	path, _ := f.Entry["path"].(string)
	if path == "" {
		return "", false
	}

	for _, a := range agents {
		if a.CWD == path || strings.HasPrefix(a.CWD, path+"/") {
			return a.PaneID, true
		}
	}
	return "", false
}
