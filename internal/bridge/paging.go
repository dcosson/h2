package bridge

import "strings"

const truncatedSuffix = "\n... (truncated)"

// SplitMessage splits text into chunks of at most maxLen characters,
// limited to at most maxPages chunks. If the text requires more than
// maxPages chunks, the last chunk is appended with a truncation suffix.
// A maxPages of 0 means unlimited pages.
//
// Splitting prefers line boundaries: when a newline exists in the second
// half of the chunk window, the split happens after that newline. If no
// newline exists past the midpoint, a hard cut is made at maxLen.
func SplitMessage(text string, maxLen, maxPages int) []string {
	if len(text) <= maxLen {
		return []string{text}
	}

	var chunks []string
	for len(text) > 0 {
		if len(text) <= maxLen {
			chunks = append(chunks, text)
			break
		}

		// If we're about to produce the last allowed page, take the rest
		// and truncate it.
		if maxPages > 0 && len(chunks) == maxPages-1 {
			chunk := text
			if len(chunk) > maxLen-len(truncatedSuffix) {
				chunk = chunk[:maxLen-len(truncatedSuffix)]
			}
			chunks = append(chunks, chunk+truncatedSuffix)
			break
		}

		cut := findSplit(text, maxLen)
		chunks = append(chunks, text[:cut])
		text = text[cut:]
	}
	return chunks
}

// findSplit returns the index at which to cut text for a chunk of at most
// maxLen characters. It prefers splitting after a newline in the second
// half of the window. If no suitable newline exists, it does a hard cut.
func findSplit(text string, maxLen int) int {
	window := text[:maxLen]
	mid := maxLen / 2

	// Search for the last newline in the window.
	cut := strings.LastIndex(window, "\n")
	if cut >= mid {
		return cut + 1 // include the newline in this chunk
	}

	// No newline past the midpoint — hard cut.
	return maxLen
}
