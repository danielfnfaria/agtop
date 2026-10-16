package store

import (
	"encoding/json"
	"errors"

	"github.com/danielfnfaria/agtop/internal/discovery"
	"github.com/danielfnfaria/agtop/internal/errno"
	"github.com/danielfnfaria/agtop/internal/ingest/tailer"
	"github.com/danielfnfaria/agtop/internal/source/opencode"
)

// errorCode is the short code of an error (never its message, which may carry content).
func errorCode(err error) string {
	var te *tailer.Error
	var se *json.SyntaxError
	switch {
	case errors.As(err, &te):
		return te.Code
	case errors.Is(err, discovery.ErrFileTooLarge):
		return "TOO_LARGE"
	case errors.As(err, &se):
		return "INVALID_JSON"
	}
	if code := opencode.ErrorCode(err); code != "" {
		return code
	}
	if code := errno.Code(err); code != "" {
		return code
	}
	return "UNKNOWN"
}
