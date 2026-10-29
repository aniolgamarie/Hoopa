//go:build !linux || !amd64

package cli

import (
	"context"
	"io"
)

func Execute(_ context.Context, _ []string, _ io.Reader, output, _ io.Writer) int {
	if output != nil {
		_, _ = io.WriteString(output, "{\"api_version\":\"v1\",\"request_id\":null,\"ok\":false,\"data\":null,\"error\":{\"code\":\"CAPABILITY_UNSUPPORTED\",\"message\":\"Hoopa requires Linux amd64\",\"retryable\":false},\"warnings\":[]}\n")
	}
	return 2
}
