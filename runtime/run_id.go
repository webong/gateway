package runtime

import (
	"context"
	"errors"
	"regexp"
)

type assignedRunIDKey struct{}

var runIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

// WithRunID preserves the Spinner-assigned ID inside the forced-command
// helper, so both sides can correlate an uncertain remote outcome.
func WithRunID(ctx context.Context, id string) (context.Context, error) {
	if !runIDPattern.MatchString(id) {
		return nil, errors.New("Shell run ID is invalid")
	}
	return context.WithValue(ctx, assignedRunIDKey{}, id), nil
}

func assignedRunID(ctx context.Context) (string, error) {
	if id, ok := ctx.Value(assignedRunIDKey{}).(string); ok && runIDPattern.MatchString(id) {
		return id, nil
	}
	return newRunID()
}
