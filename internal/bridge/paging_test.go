package bridge

import (
	"strings"
	"testing"
)

func TestSplitMessage_Short(t *testing.T) {
	chunks := SplitMessage("hello", 4096, 0)
	if len(chunks) != 1 || chunks[0] != "hello" {
		t.Errorf("expected single chunk, got %v", chunks)
	}
}

func TestSplitMessage_ExactLimit(t *testing.T) {
	msg := strings.Repeat("a", 100)
	chunks := SplitMessage(msg, 100, 0)
	if len(chunks) != 1 || chunks[0] != msg {
		t.Errorf("expected single chunk for exact limit, got %d chunks", len(chunks))
	}
}

func TestSplitMessage_SplitsAtNewlineInSecondHalf(t *testing.T) {
	// 3 lines of 40 chars each (39 + newline). Total 120 chars.
	// With maxLen=90, the last newline within the window is at position 79
	// (end of line 2). 79 >= 45 (midpoint), so we split there.
	line := strings.Repeat("x", 39) + "\n"
	msg := line + line + line
	chunks := SplitMessage(msg, 90, 0)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d: %v", len(chunks), chunks)
	}
	if chunks[0] != line+line {
		t.Errorf("chunk[0] = %q, want two lines", chunks[0])
	}
	if chunks[1] != line {
		t.Errorf("chunk[1] = %q, want one line", chunks[1])
	}
}

func TestSplitMessage_IgnoresNewlineInFirstHalf(t *testing.T) {
	// A newline early on, then a long stretch of text with no newlines.
	// The newline at position 10 is before the midpoint (50), so we
	// should hard-cut at 100 instead.
	msg := strings.Repeat("a", 10) + "\n" + strings.Repeat("b", 189)
	chunks := SplitMessage(msg, 100, 0)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if len(chunks[0]) != 100 {
		t.Errorf("chunk[0] len = %d, want 100 (hard cut)", len(chunks[0]))
	}
}

func TestSplitMessage_HardCutNoNewline(t *testing.T) {
	msg := strings.Repeat("a", 100)
	chunks := SplitMessage(msg, 30, 0)
	if len(chunks) != 4 {
		t.Fatalf("expected 4 chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if i < 3 && len(c) != 30 {
			t.Errorf("chunk[%d] len = %d, want 30", i, len(c))
		}
	}
	if chunks[3] != strings.Repeat("a", 10) {
		t.Errorf("last chunk = %q", chunks[3])
	}
}

func TestSplitMessage_Reassembles(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString(strings.Repeat("x", 79))
		b.WriteString("\n")
	}
	msg := b.String() // 8000 chars

	chunks := SplitMessage(msg, 4096, 0)
	reassembled := strings.Join(chunks, "")
	if reassembled != msg {
		t.Errorf("reassembled message doesn't match original (len %d vs %d)", len(reassembled), len(msg))
	}
	for i, chunk := range chunks {
		if len(chunk) > 4096 {
			t.Errorf("chunk[%d] len = %d, exceeds 4096", i, len(chunk))
		}
	}
}

func TestSplitMessage_MaxPages(t *testing.T) {
	// 200 chars, maxLen 30, maxPages 3. Without limit: 7 chunks.
	// With limit of 3: 3 chunks, last one truncated.
	msg := strings.Repeat("a", 200)
	chunks := SplitMessage(msg, 30, 3)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	if !strings.HasSuffix(chunks[2], truncatedSuffix) {
		t.Errorf("last chunk should end with truncation suffix, got %q", chunks[2])
	}
	// Last chunk should be at most maxLen
	if len(chunks[2]) > 30 {
		t.Errorf("last chunk len = %d, exceeds maxLen 30", len(chunks[2]))
	}
}

func TestSplitMessage_MaxPagesUnlimited(t *testing.T) {
	msg := strings.Repeat("a", 200)
	chunks := SplitMessage(msg, 30, 0)
	// Should produce all chunks without truncation.
	if len(chunks) != 7 {
		t.Fatalf("expected 7 chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk, "truncated") {
			t.Error("unlimited pages should not truncate")
		}
	}
}

func TestSplitMessage_MaxPagesOneExact(t *testing.T) {
	// Fits in one page — maxPages=1 should not truncate.
	msg := "short"
	chunks := SplitMessage(msg, 100, 1)
	if len(chunks) != 1 || chunks[0] != "short" {
		t.Errorf("expected single chunk 'short', got %v", chunks)
	}
}

func TestSplitMessage_MaxPagesOneOverflow(t *testing.T) {
	// Overflows one page — should truncate with suffix.
	msg := strings.Repeat("a", 200)
	chunks := SplitMessage(msg, 100, 1)
	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !strings.HasSuffix(chunks[0], truncatedSuffix) {
		t.Errorf("chunk should end with truncation suffix, got %q", chunks[0])
	}
	if len(chunks[0]) > 100 {
		t.Errorf("chunk len = %d, exceeds maxLen 100", len(chunks[0]))
	}
}

func TestSplitMessage_Empty(t *testing.T) {
	chunks := SplitMessage("", 100, 0)
	if len(chunks) != 1 || chunks[0] != "" {
		t.Errorf("expected single empty chunk, got %v", chunks)
	}
}

func TestSplitMessage_NewlineExactlyAtMidpoint(t *testing.T) {
	// maxLen=100, midpoint=50. Place a newline at index 50 exactly.
	// findSplit uses cut >= mid, so index 50 should qualify for line split.
	// 50 a's + \n + 49 b's = 100 chars (fits in one chunk).
	// Add more to force a split: 50 a's + \n + 99 b's = 150 chars.
	msg := strings.Repeat("a", 50) + "\n" + strings.Repeat("b", 99)
	chunks := SplitMessage(msg, 100, 0)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	// Should split after the newline at position 50 (include it in chunk 0).
	if len(chunks[0]) != 51 {
		t.Errorf("chunk[0] len = %d, want 51 (split at newline at midpoint)", len(chunks[0]))
	}
	if len(chunks[1]) != 99 {
		t.Errorf("chunk[1] len = %d, want 99", len(chunks[1]))
	}
}

func TestSplitMessage_MaxPagesExactFit(t *testing.T) {
	// Text that needs exactly maxPages chunks with no overflow — should NOT truncate.
	// 90 chars, maxLen 30, maxPages 3 → exactly 3 chunks of 30.
	msg := strings.Repeat("a", 90)
	chunks := SplitMessage(msg, 30, 3)
	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}
	for _, chunk := range chunks {
		if strings.Contains(chunk, "truncated") {
			t.Error("exact fit should not truncate")
		}
	}
	reassembled := strings.Join(chunks, "")
	if reassembled != msg {
		t.Errorf("reassembled doesn't match original")
	}
}

func TestSplitMessage_MaxPagesTruncationWithNewlines(t *testing.T) {
	// Verify that the last allowed page still respects the maxLen limit
	// when truncation kicks in, even with newline-heavy content.
	// 10 lines of 40 chars each = 400 chars. maxLen=100, maxPages=2.
	// Page 1: ~100 chars. Page 2: truncated remainder.
	line := strings.Repeat("x", 39) + "\n"
	msg := strings.Repeat(line, 10) // 400 chars
	chunks := SplitMessage(msg, 100, 2)
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}
	if !strings.HasSuffix(chunks[1], truncatedSuffix) {
		t.Errorf("last chunk should end with truncation suffix, got %q", chunks[1])
	}
	if len(chunks[1]) > 100 {
		t.Errorf("last chunk len = %d, exceeds maxLen 100", len(chunks[1]))
	}
}
