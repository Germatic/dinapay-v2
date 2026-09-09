package core

import "context"

type observabilityKey uint8

const (
	requestIDKey observabilityKey = iota
	traceparentKey
)

func WithObservability(ctx context.Context, requestID, traceparent string) context.Context {
	ctx = context.WithValue(ctx, requestIDKey, requestID)
	return context.WithValue(ctx, traceparentKey, traceparent)
}

func RequestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func Traceparent(ctx context.Context) string {
	value, _ := ctx.Value(traceparentKey).(string)
	return value
}
