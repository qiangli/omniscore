package server

import "context"

func withStudent(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, ctxStudentID, id)
}

func studentID(ctx context.Context) (int64, bool) {
	v, ok := ctx.Value(ctxStudentID).(int64)
	return v, ok
}
