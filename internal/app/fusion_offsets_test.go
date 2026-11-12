package app

import (
	"testing"
	"unicode/utf16"
	"vinyl/internal/lyrics"
)

// Word data omits spaces after Chinese characters; the sweep must still reach the actual end of the sentence.
func TestFusionWordOffsetsIncludeMissingSpaces(t *testing.T) {
	text := "心情放松摇摆 在妳三百米之外"
	words := lyrics.SpreadWords(text, 0, 100)
	offsets := fusionWordOffsets(text, words)
	if offsets[len(offsets)-1] != len(utf16.Encode([]rune(text))) || offsets[6] != 8 {
		t.Fatalf("a missing space left the final character unlit: %v", offsets)
	}
}

// Matching each word in the original text preserves repeated words, punctuation gaps and UTF-16 emoji offsets.
func TestFusionWordOffsetsFollowOriginalText(t *testing.T) {
	text := "你 你，🎵好"
	words := []lyrics.Word{{Text: "你"}, {Text: "你"}, {Text: "🎵"}, {Text: "好"}}
	offsets := fusionWordOffsets(text, words)
	for index, want := range []int{1, 3, 6, 7} {
		if offsets[index] != want {
			t.Fatalf("word %d: got %d want %d", index, offsets[index], want)
		}
	}
}
