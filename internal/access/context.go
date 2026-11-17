package access

import "context"

type limitKey struct{}

func WithLimit(ctx context.Context, deadline int64) context.Context {
	return context.WithValue(ctx, limitKey{}, deadline)
}
func Limit(ctx context.Context) int64 {
	v, _ := ctx.Value(limitKey{}).(int64)
	return v
}

type credentialKey struct{}

func WithCredential(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, credentialKey{}, token)
}
func Credential(ctx context.Context) string {
	token, _ := ctx.Value(credentialKey{}).(string)
	return token
}
