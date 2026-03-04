package main

import "testing"

func TestParseS3URL(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantBucket string
		wantPrefix string
		wantErr    bool
	}{
		{
			name:       "bucket and prefix",
			url:        "s3://my-bucket/datasets/sample/",
			wantBucket: "my-bucket",
			wantPrefix: "datasets/sample",
		},
		{
			name:       "bucket only",
			url:        "s3://my-bucket",
			wantBucket: "my-bucket",
			wantPrefix: "",
		},
		{
			name:    "invalid scheme",
			url:     "https://example.com/bucket",
			wantErr: true,
		},
		{
			name:    "missing bucket",
			url:     "s3://",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			bucket, prefix, err := parseS3URL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.url)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if bucket != tt.wantBucket {
				t.Fatalf("bucket mismatch: got %q, want %q", bucket, tt.wantBucket)
			}
			if prefix != tt.wantPrefix {
				t.Fatalf("prefix mismatch: got %q, want %q", prefix, tt.wantPrefix)
			}
		})
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{bytes: 0, want: "0 B"},
		{bytes: 1023, want: "1023 B"},
		{bytes: 1024, want: "1.0 KB"},
		{bytes: 1536, want: "1.5 KB"},
		{bytes: 1048576, want: "1.0 MB"},
	}

	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}
