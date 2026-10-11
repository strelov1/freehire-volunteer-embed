package db

import (
	"context"
	"testing"
)

func TestOpen_EmptyDatabaseURL_ReturnsError(t *testing.T) {
	_, err := Open(context.Background(), "")

	if err == nil {
		t.Fatal("expected an error for an empty database URL, got nil")
	}
}

func TestOpen_MalformedDatabaseURL_ReturnsError(t *testing.T) {
	_, err := Open(context.Background(), "not a valid postgres dsn")

	if err == nil {
		t.Fatal("expected an error for a malformed database URL, got nil")
	}
}
