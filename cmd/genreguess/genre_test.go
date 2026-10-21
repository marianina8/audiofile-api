package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

func TestNormalizeGenre(t *testing.T) {
	cases := map[string]string{
		" rock.":        "Rock",
		"Hip-hop":       "Hip Hop",
		"jazz":          "unknown",
		"ELECTRONIC\n":  "Electronic",
		"Rock and roll": "unknown",
		"":              "unknown",
	}
	for in, want := range cases {
		if got := NormalizeGenre(in); got != want {
			t.Errorf("NormalizeGenre(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBuildConverseInput(t *testing.T) {
	in := BuildConverseInput([]byte{1, 2, 3})
	if len(in.Messages) != 1 || len(in.Messages[0].Content) != 2 {
		t.Fatalf("want one message with two blocks, got %+v", in.Messages)
	}
	audio, ok := in.Messages[0].Content[0].(*types.ContentBlockMemberAudio)
	if !ok {
		t.Fatalf("first block is %T, want audio", in.Messages[0].Content[0])
	}
	if audio.Value.Format != types.AudioFormatMp3 {
		t.Errorf("format = %q, want mp3", audio.Value.Format)
	}
	if b, ok := audio.Value.Source.(*types.AudioSourceMemberBytes); !ok || len(b.Value) != 3 {
		t.Errorf("source = %#v, want 3 bytes", audio.Value.Source)
	}
	text, ok := in.Messages[0].Content[1].(*types.ContentBlockMemberText)
	if !ok {
		t.Fatalf("second block is %T, want text", in.Messages[0].Content[1])
	}
	if len(Genres) != 8 {
		t.Fatalf("want 8 genres, have %d", len(Genres))
	}
	for _, g := range Genres {
		if !contains(text.Value, g) {
			t.Errorf("prompt %q lacks genre %q", text.Value, g)
		}
	}
	if *in.InferenceConfig.MaxTokens != 10 || *in.InferenceConfig.Temperature != 0 {
		t.Errorf("inference config = %+v", *in.InferenceConfig)
	}
}

type fakeClient struct {
	reply string
	got   *bedrockruntime.ConverseInput
}

func (f *fakeClient) Converse(_ context.Context, in *bedrockruntime.ConverseInput, _ ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	f.got = in
	return &bedrockruntime.ConverseOutput{Output: &types.ConverseOutputMemberMessage{Value: types.Message{
		Role:    types.ConversationRoleAssistant,
		Content: []types.ContentBlock{&types.ContentBlockMemberText{Value: f.reply}},
	}}}, nil
}

func TestGuessUsesClient(t *testing.T) {
	f := &fakeClient{reply: " industrial."}
	got, err := Guess(context.Background(), f, []byte("x"))
	if err != nil || got != "Industrial" || f.got == nil {
		t.Fatalf("Guess = %q, %v", got, err)
	}
}

func TestAccepts(t *testing.T) {
	ok := [][2]string{{"Rock", "Rock"}, {"Industrial", "Electronic"}, {"Classical", "Ambient"}, {"Cinematic", "Ambient"}}
	for _, c := range ok {
		if !Accepts(c[0], c[1]) {
			t.Errorf("Accepts(%s,%s) = false", c[0], c[1])
		}
	}
	for _, c := range [][2]string{{"Rock", "Pop"}, {"Pop", "Electronic"}, {"Rock", "unknown"}} {
		if Accepts(c[0], c[1]) {
			t.Errorf("Accepts(%s,%s) = true", c[0], c[1])
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

type errClient struct{ err error }

func (e errClient) Converse(context.Context, *bedrockruntime.ConverseInput, ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error) {
	return nil, e.err
}

func TestErrorsAreRedacted(t *testing.T) {
	const arn = "arn:aws:sts::123456789012:assumed-role/Admin/me"
	raw := fmt.Errorf("operation error: User: %s is not authorized (acct 123456789012): %w", arn,
		&smithy.GenericAPIError{Code: "AccessDeniedException", Message: arn})
	for name, err := range map[string]error{"api": raw, "plain": errors.New("failed for " + arn)} {
		out := guessClip(context.Background(), errClient{err}, []byte("x"))
		if regexp.MustCompile(`\d{12}`).MatchString(out) || regexp.MustCompile(`arn:aws`).MatchString(out) {
			t.Errorf("%s: output leaks account data: %q", name, out)
		}
		if !regexp.MustCompile(`^error:`).MatchString(out) {
			t.Errorf("%s: output = %q, want error prefix", name, out)
		}
	}
	if out := guessClip(context.Background(), errClient{raw}, nil); out != "error: Bedrock request failed (AccessDeniedException)" {
		t.Errorf("api error output = %q", out)
	}
	if got := Redact("x arn:aws:iam::123456789012:role/a y 999999999999"); got != "x [redacted] y [redacted]" {
		t.Errorf("Redact = %q", got)
	}
}
