package server

import (
	"context"
	"strings"
	"testing"
)

func TestOSCTitleParserParsesTitles(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		title string
		found bool
	}{
		{name: "osc0 bel", input: "\x1b]0;hello\x07", title: "hello", found: true},
		{name: "osc2 bel", input: "\x1b]2;hello\x07", title: "hello", found: true},
		{name: "osc0 st", input: "\x1b]0;hello\x1b\\", title: "hello", found: true},
		{name: "utf8 title", input: "\x1b]0;✳ Крутится…\x07", title: "✳ Крутится…", found: true},
		{name: "empty title clears", input: "\x1b]0;\x07", title: "", found: true},
		{name: "surrounded by output", input: "before\x1b]2;title\x07after", title: "title", found: true},
		{name: "last title wins", input: "\x1b]0;first\x07\x1b]0;second\x07", title: "second", found: true},
		{name: "osc1 icon ignored", input: "\x1b]1;icon\x07", found: false},
		{name: "osc8 hyperlink ignored", input: "\x1b]8;;https://example.com\x1b\\link\x1b]8;;\x1b\\", found: false},
		{name: "osc52 clipboard ignored", input: "\x1b]52;c;aGVsbG8=\x07", found: false},
		{name: "non numeric osc ignored", input: "\x1b]Ptitle\x07", found: false},
		{name: "oversized param ignored", input: "\x1b]000000000;title\x07", found: false},
		{name: "csi passes through", input: "\x1b[31mred\x1b[0m", found: false},
		{name: "esc aborts osc", input: "\x1b]0;partial\x1b[31m", found: false},
		{name: "title after aborted osc", input: "\x1b]0;partial\x1b[31m\x1b]2;real\x07", title: "real", found: true},
		{name: "control bytes stripped", input: "\x1b]0;a\x01b\x02c\x07", title: "abc", found: true},
		{name: "unterminated osc keeps waiting", input: "\x1b]0;still going", found: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parser := &oscTitleParser{}
			title, found := parser.feed([]byte(test.input))
			if found != test.found || title != test.title {
				t.Fatalf("feed(%q) = (%q, %v), want (%q, %v)", test.input, title, found, test.title, test.found)
			}
		})
	}
}

func TestOSCTitleParserHandlesChunkBoundaries(t *testing.T) {
	t.Parallel()

	input := []byte("ls\r\n\x1b]0;✳ building…\x1b\\done\r\n")
	for split := 1; split < len(input); split++ {
		parser := &oscTitleParser{}
		title, found := parser.feed(input[:split])
		if !found {
			title, found = parser.feed(input[split:])
		} else if lastTitle, lastFound := parser.feed(input[split:]); lastFound {
			title = lastTitle
		}
		if !found || title != "✳ building…" {
			t.Fatalf("split at %d: title = (%q, %v), want (%q, true)", split, title, found, "✳ building…")
		}
	}
}

func TestOSCTitleParserBoundsTitleLength(t *testing.T) {
	t.Parallel()

	parser := &oscTitleParser{}
	input := "\x1b]0;" + strings.Repeat("x", oscTitleBodyLimit*2) + "\x07"
	title, found := parser.feed([]byte(input))
	if !found {
		t.Fatal("feed() found = false, want oversized title committed")
	}
	if len(title) != oscTitleBodyLimit {
		t.Fatalf("len(title) = %d, want capped at %d", len(title), oscTitleBodyLimit)
	}
}

func TestOSCTitleParserResumesAfterUnterminatedSequence(t *testing.T) {
	t.Parallel()

	parser := &oscTitleParser{}
	if _, found := parser.feed([]byte("\x1b]0;spinner frame")); found {
		t.Fatal("feed() found = true, want unterminated title pending")
	}
	title, found := parser.feed([]byte(" continues\x07"))
	if !found || title != "spinner frame continues" {
		t.Fatalf("feed() = (%q, %v), want continuation committed", title, found)
	}
}

func TestSessionProcessStatusPrefersOSCTitle(t *testing.T) {
	const fakePID = 99999999

	oldProcessSnapshot := processSnapshot
	processSnapshot = func(_ context.Context, pid int) processSnapshotResult {
		return processSnapshotResult{cwd: "/tmp/comet", foregroundCommand: "vim foo"}
	}
	t.Cleanup(func() { processSnapshot = oldProcessSnapshot })

	session := newFakeLiveSession(t, fakePID)
	session.cmd.Dir = "/tmp"
	session.attachments = make(map[*Attachment]struct{})

	session.setOSCTitle("✳ agent running…")
	status := session.ProcessStatus(context.Background())
	if status.DisplayTitle != "✳ agent running…" {
		t.Fatalf("display title = %q, want OSC title", status.DisplayTitle)
	}
	if status.ForegroundCommand != "vim foo" {
		t.Fatalf("foreground command = %q, want vim foo", status.ForegroundCommand)
	}

	session.setOSCTitle("")
	status = session.ProcessStatus(context.Background())
	if status.DisplayTitle != "vim foo" {
		t.Fatalf("display title = %q, want foreground command fallback", status.DisplayTitle)
	}
}

func TestSetOSCTitleNotifiesAttachmentsOnce(t *testing.T) {
	t.Parallel()

	session := &Session{
		attachments: make(map[*Attachment]struct{}),
		done:        make(chan struct{}),
	}
	attachment, _, err := session.Attach()
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}

	session.setOSCTitle("frame 1")
	select {
	case <-attachment.titleCh:
	default:
		t.Fatal("titleCh empty, want notification for new title")
	}

	session.setOSCTitle("frame 1")
	select {
	case <-attachment.titleCh:
		t.Fatal("titleCh notified, want unchanged title deduplicated")
	default:
	}

	session.setOSCTitle("frame 2")
	session.setOSCTitle("frame 3")
	select {
	case <-attachment.titleCh:
	default:
		t.Fatal("titleCh empty, want coalesced notification")
	}
	if got := session.OSCTitle(); got != "frame 3" {
		t.Fatalf("OSCTitle() = %q, want latest frame", got)
	}
}

func TestSetOSCTitleTruncatesLongTitles(t *testing.T) {
	t.Parallel()

	session := &Session{
		attachments: make(map[*Attachment]struct{}),
		done:        make(chan struct{}),
	}
	session.setOSCTitle(strings.Repeat("a", terminalTitleMaxLength*2))
	title := session.OSCTitle()
	if got := len([]rune(title)); got != terminalTitleMaxLength {
		t.Fatalf("len(title) = %d runes, want %d", got, terminalTitleMaxLength)
	}
	if !strings.HasSuffix(title, "…") {
		t.Fatalf("title = %q, want ellipsis suffix", title)
	}
}
