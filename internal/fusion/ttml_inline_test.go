package fusion

import (
	"context"
	"testing"
)

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
