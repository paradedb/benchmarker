package mongodb

import "testing"

func TestDatabaseFromConnStringUsesExplicitDatabase(t *testing.T) {
	got := databaseFromConnString("mongodb://localhost:27017/customdb")
	if got != "customdb" {
		t.Fatalf("expected database from connection string, got %q", got)
	}
}

func TestDatabaseFromConnStringDecodesEscapedDatabase(t *testing.T) {
	got := databaseFromConnString("mongodb://localhost:27017/custom%2Ddb")
	if got != "custom-db" {
		t.Fatalf("expected escaped database name to be decoded, got %q", got)
	}
}

func TestDatabaseFromConnStringFallsBackToBenchmark(t *testing.T) {
	got := databaseFromConnString("mongodb://localhost:27017")
	if got != "benchmark" {
		t.Fatalf("expected default benchmark database, got %q", got)
	}
}
