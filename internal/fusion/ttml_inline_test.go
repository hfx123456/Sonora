package fusion

import (
	"context"
	"testing"

	"vinyl/internal/lyrics"
)

// TTML words without an end must not reuse another word's timing or decode literal entities twice.
func TestTTMLWordTimingAndLiteralEntities(t *testing.T) {
	lines := parseTTML(`<tt><body><p begin="-1s"><span begin="1s" end="5s">A&amp;amp;</span><span begin="2s">B</span></p></body></tt>`)
	if len(lines) != 1 || lines[0].hasStart || lines[0].text != "A&amp;B" {
		t.Fatalf("unexpected TTML lines: %+v", lines)
	}
	if words := lines[0].words; len(words) != 2 || words[1].end != 2001 {
		t.Fatalf("unexpected TTML words: %+v", words)
	}
}

func TestTTMLInlineTranslationSeparated(t *testing.T) {
	input := `<tt xmlns:ttm="http://www.w3.org/ns/ttml#metadata"><body><div><p begin="1s" end="4s"><span begin="1s" end="2s">Hello</span><span ttm:role="x-translation" xml:lang="zh-CN">你好</span><span ttm:role="x-roman">ni hao</span></p></div></body></tt>`
	got, err := Fetch(context.Background(), t.TempDir(), Request{Raw: input, ParseOnly: true}, false)
	if err != nil || len(got.Lines) != 1 {
		t.Fatalf("lines=%d err=%v", len(got.Lines), err)
	}
	line := got.Lines[0]
	if line.Text != "Hello" {
		t.Fatalf("original=%q, want %q", line.Text, "Hello")
	}
	if line.Translation != "你好" {
		t.Fatalf("translation=%q, want %q", line.Translation, "你好")
	}
}

// Nested backing-vocal spans (ttm:role="x-bg") must preserve outer text and child word timings without breaking span state.
func TestTTMLNestedBackingVocalSpans(t *testing.T) {
	input := `<tt xmlns:ttm="http://www.w3.org/ns/ttml#metadata"><body><div><p begin="10s" end="15s"><span begin="10s" end="12s">Hello </span><span ttm:role="x-bg">(<span begin="12s" end="13s">oh</span><span begin="13s" end="14s"> yeah</span>)</span></p></div></body></tt>`
	lines := parseTTML(input)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	if lines[0].text != "Hello (oh yeah)" {
		t.Fatalf("text = %q, want %q", lines[0].text, "Hello (oh yeah)")
	}
	if len(lines[0].words) != 3 || lines[0].words[0].text != "Hello " || lines[0].words[1].text != "oh" || lines[0].words[2].text != " yeah" {
		t.Fatalf("unexpected words: %+v", lines[0].words)
	}
}

// A tiny credit-only or sparse word-timed lyric must never beat a complete synced LRC.
func TestBetterResultPrefersFullSyncedOverTinyWordSnippet(t *testing.T) {
	full := Result{Source: "lrc"}
	for i := 0; i < 40; i++ {
		full.Lines = append(full.Lines, lyrics.Line{Text: "Real lyric line"})
	}
	sparse := Result{Source: "yrc", Lines: []lyrics.Line{
		{Text: "作词 : Test", Words: []lyrics.Word{{Text: "作词"}}},
		{Text: "作曲 : Test", Words: []lyrics.Word{{Text: "作曲"}}},
		{Text: "One real line", Words: []lyrics.Word{{Text: "One"}}},
	}}
	if betterResult(sparse, full) {
		t.Fatal("sparse word-timed snippet wrongly beat a 40-line synced lyric")
	}
	if !betterResult(full, sparse) {
		t.Fatal("40-line synced lyric should beat sparse word-timed snippet")
	}
}
