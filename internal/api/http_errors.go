package api

import "io"

const maxCLIHTTPErrorBytes int64 = 64 << 10

// readBoundedHTTPErrorBody limits untrusted control-plane failures before the
// shared parser projects their safe structured fields.
func readBoundedHTTPErrorBody(body io.Reader) []byte {
	payload, _, _ := readBoundedCLIHTTPBody(body)
	return payload
}

// readBoundedCLIHTTPBody reports transport truncation separately from the size
// ceiling so mutation callers never mistake an incomplete response for proof.
func readBoundedCLIHTTPBody(body io.Reader) ([]byte, bool, error) {
	payload, err := io.ReadAll(io.LimitReader(body, maxCLIHTTPErrorBytes+1))
	// A body beyond the bounded contract is unusable even if its prefix is valid JSON.
	if int64(len(payload)) > maxCLIHTTPErrorBytes {
		return payload[:maxCLIHTTPErrorBytes], true, err
	}
	return payload, false, err
}
