package api

import "io"

// readImportReviewError admits bounded multi-operation diagnostics without increasing unrelated error limits.
func readImportReviewError(body io.Reader) []byte {
	const limit = 1 << 20
	payload, err := io.ReadAll(io.LimitReader(body, limit+1))
	// An incomplete or oversized envelope cannot be mistaken for an authoritative review.
	if err != nil || len(payload) > limit {
		return nil
	}
	return payload
}
