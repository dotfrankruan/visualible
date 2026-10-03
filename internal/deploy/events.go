package deploy

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/dotfrankruan/visualible/internal/ir"
)

// NormalizeCallbackEvent converts one JSON line emitted by the
// visualible_events Ansible callback plugin into a typed deployment
// event. Unknown or malformed lines yield nil (callers skip them) except
// where a message can be salvaged as a log line.
func NormalizeCallbackEvent(deploymentID string, line []byte) *ir.DeploymentEvent {
	var raw struct {
		Event     string          `json:"event"`
		Ts        float64         `json:"ts"`
		Play      string          `json:"play"`
		Task      string          `json:"task"`
		Host      string          `json:"host"`
		Changed   bool            `json:"changed"`
		IsHandler bool            `json:"is_handler"`
		Message   string          `json:"message"`
		Summary   json.RawMessage `json:"summary"`
	}
	if err := json.Unmarshal(line, &raw); err != nil || raw.Event == "" {
		return nil
	}
	ev := NewEvent(deploymentID, "")
	if raw.Ts > 0 {
		sec := int64(raw.Ts)
		ev.Timestamp = time.Unix(sec, int64((raw.Ts-float64(sec))*1e9)).UTC()
	}
	ev.Play = raw.Play
	ev.Task = raw.Task
	ev.Host = raw.Host
	ev.Changed = raw.Changed
	ev.Message = raw.Message

	switch raw.Event {
	case "playbook.start":
		ev.Type = ir.EventDeploymentStarted
	case "play.start":
		ev.Type = ir.EventPlayStarted
	case "task.start":
		if raw.IsHandler {
			ev.Type = ir.EventHandlerStarted
		} else {
			ev.Type = ir.EventTaskStarted
		}
	case "task.ok":
		ev.Type = ir.EventTaskOK
		ev.Status = "ok"
	case "task.changed":
		ev.Type = ir.EventTaskChanged
		ev.Status = "changed"
	case "task.skipped":
		ev.Type = ir.EventTaskSkipped
		ev.Status = "skipped"
	case "task.failed":
		ev.Type = ir.EventTaskFailed
		ev.Status = "failed"
	case "host.unreachable":
		ev.Type = ir.EventHostUnreachable
		ev.Status = "unreachable"
	case "stats":
		// The manager turns stats + exit code into deployment.finished;
		// the summary is attached as the message payload.
		ev.Type = ir.EventDeploymentFinished
		if len(raw.Summary) > 0 {
			ev.Message = summarizeStats(raw.Summary)
		}
	default:
		return nil
	}
	return &ev
}

// summarizeStats compacts the per-host stats summary into a single
// human-readable line for the finished event message.
func summarizeStats(raw json.RawMessage) string {
	var summary map[string]map[string]int
	if err := json.Unmarshal(raw, &summary); err != nil {
		return ""
	}
	var b strings.Builder
	for host, counts := range summary {
		if b.Len() > 0 {
			b.WriteString("; ")
		}
		b.WriteString(host)
		b.WriteString(": ")
		first := true
		for _, k := range []string{"ok", "changed", "failures", "unreachable", "skipped"} {
			if n := counts[k]; n > 0 {
				if !first {
					b.WriteString(" ")
				}
				b.WriteString(k)
				b.WriteByte('=')
				b.WriteString(itoa(n))
				first = false
			}
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [12]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
