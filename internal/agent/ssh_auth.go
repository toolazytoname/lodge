package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/netip"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/toolazytoname/lodge/internal/shared"
)

const (
	sshAuthWindow            = 10 * time.Minute
	maximumSSHAuthSources    = 20
	maximumSSHFailures       = 1_000_000
	maximumSSHJournalEntries = 50_000
	maximumSSHAuthLogEntries = 1_000_000
	maximumSSHAuthMessage    = 4096
)

var sshFailureAddressPattern = regexp.MustCompile(`(?i)(?:failed (?:password|publickey|keyboard-interactive(?:/pam)?) for (?:invalid user )?.* from|maximum authentication attempts exceeded for (?:invalid user )?.* from) ([^ ]+) port [0-9]+`)

type sshJournalEntry struct {
	Message json.RawMessage `json:"MESSAGE"`
}

func collectSSHAuthSummary() (*shared.SSHAuthSummary, string) {
	stdout, _, err := runPrivileged(sshAuthCommand)
	if err != nil {
		return nil, "采集 SSH 认证失败摘要失败: " + err.Error()
	}
	var summary shared.SSHAuthSummary
	if err := json.Unmarshal(stdout, &summary); err != nil || validateSSHAuthSummary(summary) != nil {
		return nil, "SSH 认证失败摘要格式无效"
	}
	return &summary, ""
}

func validateSSHAuthSummary(summary shared.SSHAuthSummary) error {
	start, err := time.Parse(time.RFC3339, summary.WindowStart)
	if err != nil {
		return errors.New("SSH summary has invalid start time")
	}
	end, err := time.Parse(time.RFC3339, summary.WindowEnd)
	if err != nil || end.Sub(start) < 5*time.Minute || end.Sub(start) > 15*time.Minute {
		return errors.New("SSH summary has invalid time window")
	}
	if summary.Sources == nil || summary.FailedTotal < 0 || summary.FailedTotal > maximumSSHFailures || len(summary.Sources) > maximumSSHAuthSources {
		return errors.New("SSH summary count exceeds limit")
	}
	if (summary.FailedTotal == 0) != (len(summary.Sources) == 0) {
		return errors.New("SSH summary total and sources are inconsistent")
	}
	seen := make(map[netip.Addr]struct{}, len(summary.Sources))
	sourceTotal := 0
	for _, source := range summary.Sources {
		address, err := netip.ParseAddr(source.Address)
		if err != nil || address.String() != source.Address || source.Count < 1 || source.Count > maximumSSHFailures {
			return errors.New("SSH summary source is invalid")
		}
		if _, duplicate := seen[address]; duplicate {
			return errors.New("SSH summary source is duplicated")
		}
		seen[address] = struct{}{}
		sourceTotal += source.Count
	}
	if sourceTotal > summary.FailedTotal {
		return errors.New("SSH summary source counts exceed total")
	}
	return nil
}

func summarizeSSHJournal(content []byte, start, end time.Time) (shared.SSHAuthSummary, error) {
	counts := make(map[netip.Addr]int)
	total := 0
	entries := 0
	decoder := json.NewDecoder(bytes.NewReader(content))
	for {
		var entry sshJournalEntry
		if err := decoder.Decode(&entry); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return shared.SSHAuthSummary{}, errors.New("SSH journal output is invalid")
		}
		entries++
		if entries > maximumSSHJournalEntries {
			return shared.SSHAuthSummary{}, errors.New("SSH journal entry count exceeds limit")
		}
		var message string
		if len(entry.Message) == 0 || json.Unmarshal(entry.Message, &message) != nil || len(message) > maximumSSHAuthMessage {
			continue
		}
		if addSSHFailure(counts, message) {
			total++
		}
		if total > maximumSSHFailures {
			return shared.SSHAuthSummary{}, errors.New("SSH failure count exceeds limit")
		}
	}
	return buildSSHAuthSummary(counts, total, start, end), nil
}

func summarizeSSHTextLog(content []byte, start, end time.Time) (shared.SSHAuthSummary, error) {
	counts := make(map[netip.Addr]int)
	total := 0
	entries := 0
	parsedTimestamps := 0
	var oldest time.Time
	for len(content) > 0 {
		line := content
		if newline := bytes.IndexByte(content, '\n'); newline >= 0 {
			line = content[:newline]
			content = content[newline+1:]
		} else {
			content = nil
		}
		if len(line) == 0 {
			continue
		}
		entries++
		if entries > maximumSSHAuthLogEntries {
			return shared.SSHAuthSummary{}, errors.New("SSH authentication log entry count exceeds limit")
		}
		loggedAt, ok := parseSSHAuthLogTime(line, end)
		if !ok {
			continue
		}
		parsedTimestamps++
		if oldest.IsZero() || loggedAt.Before(oldest) {
			oldest = loggedAt
		}
		if loggedAt.Before(start) || loggedAt.After(end.Add(time.Minute)) {
			continue
		}
		if len(line) > maximumSSHAuthMessage {
			return shared.SSHAuthSummary{}, errors.New("SSH authentication log line exceeds limit")
		}
		if addSSHFailure(counts, string(line)) {
			total++
		}
		if total > maximumSSHFailures {
			return shared.SSHAuthSummary{}, errors.New("SSH failure count exceeds limit")
		}
	}
	if parsedTimestamps == 0 && entries > 0 {
		return shared.SSHAuthSummary{}, errors.New("SSH authentication log timestamp format is unsupported")
	}
	if oldest.IsZero() || oldest.After(start) {
		return shared.SSHAuthSummary{}, errors.New("SSH authentication log tail does not cover the complete window")
	}
	return buildSSHAuthSummary(counts, total, start, end), nil
}

func parseSSHAuthLogTime(line []byte, reference time.Time) (time.Time, bool) {
	if space := bytes.IndexByte(line, ' '); space > 0 {
		if parsed, err := time.Parse(time.RFC3339Nano, string(line[:space])); err == nil {
			return parsed, true
		}
	}
	if len(line) < len("Jan  2 15:04:05") {
		return time.Time{}, false
	}
	localReference := reference.In(time.Local)
	parsed, err := time.ParseInLocation("Jan _2 15:04:05", string(line[:len("Jan  2 15:04:05")]), time.Local)
	if err != nil {
		return time.Time{}, false
	}
	parsed = time.Date(localReference.Year(), parsed.Month(), parsed.Day(), parsed.Hour(), parsed.Minute(), parsed.Second(), 0, time.Local)
	if parsed.After(localReference.Add(24 * time.Hour)) {
		parsed = parsed.AddDate(-1, 0, 0)
	}
	return parsed, true
}

func addSSHFailure(counts map[netip.Addr]int, message string) bool {
	match := sshFailureAddressPattern.FindStringSubmatch(message)
	if len(match) != 2 {
		return false
	}
	address, err := netip.ParseAddr(strings.TrimSpace(match[1]))
	if err != nil {
		return false
	}
	counts[address.Unmap()]++
	return true
}

func buildSSHAuthSummary(counts map[netip.Addr]int, total int, start, end time.Time) shared.SSHAuthSummary {
	type sourceCount struct {
		address netip.Addr
		count   int
	}
	ranked := make([]sourceCount, 0, len(counts))
	for address, count := range counts {
		ranked = append(ranked, sourceCount{address: address, count: count})
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].count != ranked[right].count {
			return ranked[left].count > ranked[right].count
		}
		return ranked[left].address.Less(ranked[right].address)
	})
	if len(ranked) > maximumSSHAuthSources {
		ranked = ranked[:maximumSSHAuthSources]
	}
	summary := shared.SSHAuthSummary{
		WindowStart: start.UTC().Format(time.RFC3339), WindowEnd: end.UTC().Format(time.RFC3339),
		FailedTotal: total, Sources: make([]shared.SSHAuthSource, 0, len(ranked)),
	}
	for _, source := range ranked {
		summary.Sources = append(summary.Sources, shared.SSHAuthSource{Address: source.address.String(), Count: source.count})
	}
	return summary
}
