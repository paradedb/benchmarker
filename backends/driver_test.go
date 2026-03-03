package backends

import "testing"

func TestSplitSQLStatements(t *testing.T) {
	tests := []struct {
		name   string
		script string
		want   []string
	}{
		{
			name:   "simple statements",
			script: "SELECT 1; SELECT 2;",
			want:   []string{"SELECT 1", "SELECT 2"},
		},
		{
			name:   "semicolon in string literal",
			script: "INSERT INTO logs(message) VALUES ('first;second');\nSELECT 1;",
			want: []string{
				"INSERT INTO logs(message) VALUES ('first;second')",
				"SELECT 1",
			},
		},
		{
			name:   "line and block comments",
			script: "-- comment; with semicolon\nSELECT 1;\n/* block; comment */\nSELECT 2;",
			want: []string{
				"-- comment; with semicolon\nSELECT 1",
				"/* block; comment */\nSELECT 2",
			},
		},
		{
			name:   "dollar quoted function body",
			script: "CREATE FUNCTION f() RETURNS void AS $fn$\nBEGIN\n  PERFORM 'a;b';\nEND;\n$fn$ LANGUAGE plpgsql;\nSELECT 1;",
			want: []string{
				"CREATE FUNCTION f() RETURNS void AS $fn$\nBEGIN\n  PERFORM 'a;b';\nEND;\n$fn$ LANGUAGE plpgsql",
				"SELECT 1",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SplitSQLStatements(tt.script)
			if len(got) != len(tt.want) {
				t.Fatalf("statement count mismatch: got %d, want %d (%v)", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("statement %d mismatch:\n got: %q\nwant: %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}
