package main

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime/types"
	"github.com/aws/smithy-go"
)

// Genres is the fixed list the model may answer with.
var Genres = []string{"Electronic", "Industrial", "Rock", "Pop", "Hip Hop", "Cinematic", "Ambient", "Classical"}

const (
	// ModelID is the Bedrock model used for the one-off genre test.
	ModelID = "mistral.voxtral-small-24b-2507"
	// Unknown is what NormalizeGenre returns for any reply outside Genres.
	Unknown = "unknown"
)

// acceptable lists extra genres that count as correct in --validate.
var acceptable = map[string][]string{
	"Industrial": {"Electronic"},
	"Classical":  {"Ambient"},
	"Cinematic":  {"Ambient"},
}

// Accepts reports whether guessed is correct for a track whose filename says
// expected.
func Accepts(expected, guessed string) bool {
	if strings.EqualFold(expected, guessed) {
		return true
	}
	for _, g := range acceptable[expected] {
		if strings.EqualFold(g, guessed) {
			return true
		}
	}
	return false
}

// NormalizeGenre maps a free-text model reply onto Genres, ignoring case,
// surrounding punctuation and hyphens. Anything else is "unknown".
func NormalizeGenre(reply string) string {
	clean := func(s string) string {
		s = strings.ToLower(s)
		s = strings.NewReplacer("-", " ", "_", " ").Replace(s)
		s = strings.Trim(s, " \t\r\n.,;:!?\"'`*")
		return strings.Join(strings.Fields(s), " ")
	}
	r := clean(reply)
	for _, g := range Genres {
		if r == clean(g) {
			return g
		}
	}
	return Unknown
}

// Prompt is the text block that follows the audio.
func Prompt() string {
	return "Which one genre best fits this music? Answer with exactly one of: " +
		strings.Join(Genres, ", ") + "."
}

// BuildConverseInput builds the Converse request: one audio block (mp3 bytes)
// then one text block.
func BuildConverseInput(mp3 []byte) *bedrockruntime.ConverseInput {
	return &bedrockruntime.ConverseInput{
		ModelId: aws.String(ModelID),
		Messages: []types.Message{{
			Role: types.ConversationRoleUser,
			Content: []types.ContentBlock{
				&types.ContentBlockMemberAudio{Value: types.AudioBlock{
					Format: types.AudioFormatMp3,
					Source: &types.AudioSourceMemberBytes{Value: mp3},
				}},
				&types.ContentBlockMemberText{Value: Prompt()},
			},
		}},
		InferenceConfig: &types.InferenceConfiguration{
			MaxTokens:   aws.Int32(10),
			Temperature: aws.Float32(0),
		},
	}
}

// Converser is the part of the Bedrock runtime client this tool uses.
type Converser interface {
	Converse(ctx context.Context, in *bedrockruntime.ConverseInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.ConverseOutput, error)
}

// Guess sends the clip to the model and returns a normalized genre.
func Guess(ctx context.Context, c Converser, mp3 []byte) (string, error) {
	out, err := c.Converse(ctx, BuildConverseInput(mp3))
	if err != nil {
		return "", err
	}
	msg, ok := out.Output.(*types.ConverseOutputMemberMessage)
	if !ok {
		return "", fmt.Errorf("unexpected converse output %T", out.Output)
	}
	var sb strings.Builder
	for _, b := range msg.Value.Content {
		if t, ok := b.(*types.ContentBlockMemberText); ok {
			sb.WriteString(t.Value)
		}
	}
	return NormalizeGenre(sb.String()), nil
}

var (
	arnRe     = regexp.MustCompile(`arn:aws[^\s]*`)
	accountRe = regexp.MustCompile(`\d{12}`)
)

// Redact removes ARNs and 12-digit account numbers from text before printing.
func Redact(s string) string {
	return accountRe.ReplaceAllString(arnRe.ReplaceAllString(s, "[redacted]"), "[redacted]")
}

// SafeError describes a failure for printing: only the API error code and a
// generic message, never the raw error text (it can carry ARNs and account ids).
func SafeError(err error) string {
	var api smithy.APIError
	if errors.As(err, &api) {
		return Redact("error: Bedrock request failed (" + api.ErrorCode() + ")")
	}
	return "error: request failed"
}
