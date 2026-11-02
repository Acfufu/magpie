package sessions

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// The database fixture uses ZCode's session/message/part shape, including
// modelId and semantics.origin, rather than OpenCode 2's tables.
func TestZCodeConversationDetails(t *testing.T) {
	setupAgents(t)
	s := find(t, List(0), "zcode", "ses_zcA")
	if !s.Transcript || len(s.Subagents) != 1 || s.Subagents[0].ID != "ses_zcB" {
		t.Fatalf("ZCode conversation and child must be available: transcript=%v subagents=%+v", s.Transcript, s.Subagents)
	}
	tr, err := TranscriptOf(s)
	if err != nil || !strings.Contains(transcriptText(t, tr), "Port the   parser") {
		t.Fatalf("ZCode's own transcript: %+v, %v", tr, err)
	}
	for _, p := range tr.Parts {
		if strings.Contains(p.Text, "Remember the house rules") && (p.Kind != "context" || p.Role != "system") {
			t.Fatalf("ZCode's system reminder isn't a user's prompt: %+v", p)
		}
	}
	if _, err := SubagentTranscript(s, "ses_zcB"); err != nil {
		t.Fatalf("ZCode child transcript: %v", err)
	}
	if _, hit, err := SearchSession(context.Background(), s, "parser"); err != nil || !hit {
		t.Fatalf("search ZCode conversation: hit=%v err=%v", hit, err)
	}
	var md bytes.Buffer
	if err := WriteMarkdown(&md, s, "ZCode"); err != nil || !strings.Contains(md.String(), "Port the   parser") {
		t.Fatalf("export ZCode conversation: %q %v", md.String(), err)
	}
}
